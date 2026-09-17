/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/rpc"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

// The method names and payload types live in the dynamic package now, so that the
// ingester and this server cannot drift apart.  They are aliased here only to keep the
// handler signatures readable.
const (
	MethodRegisterKinds = dynamic.MethodRegisterKinds
	MethodListRunners   = dynamic.MethodListRunners
	MethodApplyConfig   = dynamic.MethodApplyConfig
	MethodReportStatus  = dynamic.MethodReportStatus
)

// pushTimeout bounds a call down to an ingester.  A wedged ingester must not hold an HTTP
// handler open.
const pushTimeout = 10 * time.Second

// API is the server side of the dynamic config protocol.  It owns the store and the set
// of connected ingesters.
type API struct {
	store *Store
	lgr   *log.Logger

	mtx       sync.RWMutex
	ingesters map[uuid.UUID]*rpc.Session
	classes   map[uuid.UUID]string
}

func NewAPI(store *Store, lgr *log.Logger) *API {
	if lgr == nil {
		lgr = log.NewDiscardLogger()
	}
	return &API{
		store:     store,
		lgr:       lgr,
		ingesters: map[uuid.UUID]*rpc.Session{},
		classes:   map[uuid.UUID]string{},
	}
}

// Mux builds the method set we expose to ingesters.
func (a *API) Mux() (m *rpc.Mux, err error) {
	m = rpc.NewMux()
	if err = m.Register(MethodRegisterKinds, a.registerKinds); err != nil {
		return
	}
	if err = m.Register(MethodListRunners, a.listRunners); err != nil {
		return
	}
	err = m.Register(MethodReportStatus, a.reportStatus)
	return
}

// OnSession tracks a connected ingester for the life of its session, so the UI has
// somewhere to push a config change.
func (a *API) OnSession(s *rpc.Session) {
	id := s.ID()
	a.mtx.Lock()
	// a reconnect replaces the old handle, the old session is already dead
	a.ingesters[id] = s
	a.mtx.Unlock()
	a.lgr.Info("ingester connected", log.KV("id", id), log.KV("class", s.Class()),
		log.KV("remote", s.RemoteAddr()))

	<-s.Done()

	a.mtx.Lock()
	// only drop it if it is still ours, a reconnect may have replaced us already
	if cur, ok := a.ingesters[id]; ok && cur == s {
		delete(a.ingesters, id)
	}
	a.mtx.Unlock()
	a.lgr.Info("ingester disconnected", log.KV("id", id), log.KVErr(s.Err()))
}

// Connected lists the ingesters we can currently push to.
func (a *API) Connected() (r []*rpc.Session) {
	a.mtx.RLock()
	defer a.mtx.RUnlock()
	for _, s := range a.ingesters {
		r = append(r, s)
	}
	return
}

// registerKinds stores everything an ingester says it can run, keyed by that ingester.
//
// The identity comes from the session rather than from the body.  The handshake is what
// proves who the peer is, so a body that claims a different UUID is ignored rather than
// believed, otherwise any authenticated ingester could overwrite another's registrations.
func (a *API) registerKinds(ctx context.Context, params json.RawMessage) (any, error) {
	id, ok := sessionID(ctx)
	if !ok {
		return nil, fmt.Errorf("no session identity")
	}
	var req dynamic.RegisterKindsRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("bad registration %w", err)
	}
	// the class comes from the session too, for the same reason the UUID does
	class := req.Class
	if sess, ok := rpc.SessionFrom(ctx); ok && sess.Class() != `` {
		class = sess.Class()
	}
	if err := a.store.ReplaceKinds(id, class, req.Kinds); err != nil {
		return nil, err
	}
	a.mtx.Lock()
	a.classes[id] = class
	a.mtx.Unlock()
	a.lgr.Info("registered kinds", log.KV("ingester", id), log.KV("class", class),
		log.KV("count", len(req.Kinds)))
	return map[string]any{`ok`: true}, nil
}

// listRunners hands an ingester the configurations it should be running: the ones whose
// kind it can actually run and whose assignment names it, its class, or nobody.
func (a *API) listRunners(ctx context.Context, params json.RawMessage) (any, error) {
	var q dynamic.RunnerQuery
	if len(params) > 0 {
		if err := json.Unmarshal(params, &q); err != nil {
			return nil, fmt.Errorf("bad query %w", err)
		}
	}
	// the identity is the session's, not whatever the body asked for.  A configuration
	// pinned to specific UUIDs or classes would be trivially reachable otherwise, an
	// ingester would just claim to be someone it is not.
	if sess, ok := rpc.SessionFrom(ctx); ok {
		q.ID = sess.ID()
		q.Class = sess.Class()
	}
	all, err := a.store.Runners()
	if err != nil {
		return nil, err
	}
	var set dynamic.RunnerSet
	for _, rd := range all {
		if q.Matches(rd) {
			set.Runners = append(set.Runners, rd)
		}
	}
	a.lgr.Info("listed runners", log.KV("ingester", q.ID), log.KV("class", q.Class),
		log.KV("kinds", len(q.Kinds)), log.KV("matched", len(set.Runners)))
	return set, nil
}

// reportStatus records what an ingester makes of the configurations it was handed.
//
// The identity comes from the session rather than the body, for the same reason
// registerKinds takes it from there: otherwise any authenticated ingester could plant a
// failure against another one's name, or clear a real one.
//
// The report is the complete set for that ingester, so it replaces rather than merges,
// which is what lets a runner that has come good clear itself.
func (a *API) reportStatus(ctx context.Context, params json.RawMessage) (any, error) {
	id, ok := sessionID(ctx)
	if !ok {
		return nil, fmt.Errorf("no session identity")
	}
	var req dynamic.ReportStatusRequest
	if len(params) > 0 {
		if err := json.Unmarshal(params, &req); err != nil {
			return nil, fmt.Errorf("bad status report %w", err)
		}
	}
	if err := a.store.ReplaceStatuses(id, req.Statuses); err != nil {
		return nil, err
	}
	var bad int
	for _, rs := range req.Statuses {
		if !rs.OK() {
			bad++
		}
	}
	a.lgr.Info("recorded runner status", log.KV("ingester", id),
		log.KV("reported", len(req.Statuses)), log.KV("failing", bad))
	return map[string]any{`ok`: true}, nil
}

// push sends a configuration down to the connected ingesters it is actually meant for,
// and reports which ones took it.
//
// The assignment is applied here, not just at poll time.  A push is how a change reaches
// an ingester promptly, so if it ignored the assignment a configuration pinned to one
// ingester would still land on every other one the moment it was saved, and the pinning
// would only work until the next save.  The rule is the same RunnerQuery.Matches the poll
// uses, so the two paths cannot disagree.
//
// With no ingester connected, or none that the configuration is meant for, it is simply
// stored and picked up on the next poll.  That is a normal state, not a failure.
func (a *API) push(rd dynamic.RunnerDefinition) (delivered int, errs []string) {
	sessions := a.Connected()
	if len(sessions) == 0 {
		return
	}
	ctx, cf := context.WithTimeout(context.Background(), pushTimeout)
	defer cf()
	for _, s := range sessions {
		if !a.targeted(s, rd) {
			continue
		}
		if err := s.Call(ctx, dynamic.MethodApplyConfig, rd, nil); err != nil {
			a.lgr.Error("failed to push config", log.KV("id", s.ID()),
				log.KV("kind", rd.Kind), log.KV("name", rd.Name), log.KVErr(err))
			errs = append(errs, fmt.Sprintf("%v: %v", s.ID(), err))
			continue
		}
		delivered++
	}
	return
}

// targeted reports whether a configuration is meant for one connected ingester, using the
// kinds that ingester registered and the identity its session authenticated with.
func (a *API) targeted(s *rpc.Session, rd dynamic.RunnerDefinition) bool {
	kinds, err := a.store.IngesterKinds(s.ID())
	if err != nil {
		a.lgr.Error("failed to read an ingester's kinds", log.KV("id", s.ID()), log.KVErr(err))
		return false // we cannot show it is meant for them, so do not send it
	}
	q := dynamic.RunnerQuery{ID: s.ID(), Class: s.Class()}
	for _, k := range kinds {
		q.Kinds = append(q.Kinds, k.Kind)
	}
	return q.Matches(rd)
}

// sessionID pulls the calling ingester's UUID out of the handler context.  The identity
// comes from the authenticated session rather than from the request body, so an ingester
// cannot register kinds or claim configurations on behalf of another.
func sessionID(ctx context.Context) (id uuid.UUID, ok bool) {
	var s *rpc.Session
	if s, ok = rpc.SessionFrom(ctx); !ok {
		return
	}
	id = s.ID()
	ok = id != uuid.Nil()
	return
}
