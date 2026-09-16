/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/rpc"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

const (
	// backoffBase is the first wait after a failed connection, and the step the backoff
	// doubles from.
	backoffBase = time.Second

	// backoffMax caps the wait between attempts.  A webserver that has been down for an
	// hour should still be picked up within a minute of coming back.
	backoffMax = time.Minute

	// callTimeout bounds a single RPC.  A wedged server must not stall the loop forever.
	callTimeout = 30 * time.Second

	rpcPath = `/api/ingester/hosted`
)

// backoff produces the wait after n consecutive failures, capped and jittered.
//
// The jitter matters more than the cap: without it every ingester that lost the same
// webserver reconnects in lockstep and hammers it the moment it comes back.  Full jitter
// over the interval is the standard answer and costs nothing.
func backoff(n int) time.Duration {
	d := backoffBase << min(n, 16) // 16 doublings is already far past the cap
	if d > backoffMax || d <= 0 {  // <= 0 catches the shift overflowing
		d = backoffMax
	}
	// full jitter, anywhere in (0, d]
	return time.Duration(rand.Int63n(int64(d))) + time.Millisecond
}

// start launches the background client.  It returns immediately, the loop owns its own
// lifetime and stops when the manager's context is cancelled.
func (dcm *DynamicConfigManager) start() {
	dcm.wg.Go(dcm.run)
}

// run keeps a session to a webserver up, reconnecting with backoff, for as long as the
// manager lives.
func (dcm *DynamicConfigManager) run() {
	var fails int
	for {
		if dcm.ctx.Err() != nil {
			return
		}
		// rotate through the configured webservers so that a dead one does not pin us
		raw := dcm.Webserver[fails%len(dcm.Webserver)]
		endpoint, err := url.Parse(raw)
		if err != nil {
			fails++
			dcm.lgr.Error("dynamic config webserver endpoint is unusable",
				log.KV("webserver", raw), log.KVErr(err))
			if !dcm.sleep(backoff(fails)) {
				return
			}
			continue
		}
		endpoint.Path = rpcPath

		sess, err := dcm.dial(endpoint.String())
		if err != nil {
			fails++
			wait := backoff(fails)
			dcm.lgr.Warn("dynamic config connection failed, backing off",
				log.KV("webserver", endpoint.String()), log.KV("attempt", fails),
				log.KV("retry-in", wait), log.KVErr(err))
			if !dcm.sleep(wait) {
				return
			}
			continue
		}
		dcm.lgr.Info("dynamic config connected", log.KV("webserver", endpoint.String()))
		fails = 0 // a successful connection resets the backoff

		if err = dcm.session(sess); err != nil && dcm.ctx.Err() == nil {
			dcm.lgr.Warn("dynamic config session ended", log.KV("webserver", endpoint.String()), log.KVErr(err))
		}
		sess.Close()

		// a session that came up and then dropped still backs off, otherwise a server
		// that accepts and immediately hangs up becomes a tight reconnect loop
		fails++
		if !dcm.sleep(backoff(fails)) {
			return
		}
	}
}

// sleep waits, and reports false if the manager was shut down instead.
func (dcm *DynamicConfigManager) sleep(d time.Duration) bool {
	tmr := time.NewTimer(d)
	defer tmr.Stop()
	select {
	case <-dcm.ctx.Done():
		return false
	case <-tmr.C:
		return true
	}
}

// dial brings up an authenticated session to one webserver.
func (dcm *DynamicConfigManager) dial(endpoint string) (*rpc.Session, error) {
	ctx, cf := context.WithTimeout(dcm.ctx, callTimeout)
	defer cf()
	return rpc.Dial(ctx, rpc.ClientConfig{
		Webserver: endpoint,
		Path:      RPCPath,
		Token:     dcm.Auth_Token,
		Class:     dcm.Class,
		ID:        dcm.guid,
		Logger:    dcm.lgr,
		Handlers:  dcm.handlers,
	})
}

// session runs one connection: declare what we can run, then poll for what we should be
// running until the session drops or we are shut down.
func (dcm *DynamicConfigManager) session(sess *rpc.Session) (err error) {
	if err = dcm.sendRegistrations(sess); err != nil {
		return fmt.Errorf("failed to register kinds %w", err)
	}

	tckr := time.NewTicker(dcm.pollInterval)
	defer tckr.Stop()
	for {
		if err = dcm.refresh(sess); err != nil {
			return
		}
		select {
		case <-dcm.ctx.Done():
			return dcm.ctx.Err()
		case <-sess.Done():
			return sess.Err()
		case <-dcm.nudge:
			// something changed locally, usually a newly registered kind, so redeclare
			// before asking again
			if err = dcm.sendRegistrations(sess); err != nil {
				return fmt.Errorf("failed to register kinds %w", err)
			}
		case <-tckr.C:
		}
	}
}

// sendRegistrations declares everything this ingester can run.  The full set goes every
// time, so the server replaces rather than merges and a kind that is gone really goes.
func (dcm *DynamicConfigManager) sendRegistrations(sess *rpc.Session) (err error) {
	req := RegisterKindsRequest{
		ID:    dcm.guid,
		Class: dcm.Class,
		Kinds: dcm.Kinds(),
	}
	ctx, cf := context.WithTimeout(dcm.ctx, callTimeout)
	defer cf()
	if err = sess.Call(ctx, MethodRegisterKinds, req, nil); err != nil {
		return
	}
	dcm.lgr.Info("dynamic config registered kinds", log.KV("count", len(req.Kinds)))
	return
}

// refresh asks the server what we should be running and applies the answer.
func (dcm *DynamicConfigManager) refresh(sess *rpc.Session) (err error) {
	q := RunnerQuery{
		ID:    dcm.guid,
		Class: dcm.Class,
		Kinds: dcm.KindNames(),
	}
	if len(q.Kinds) == 0 {
		// nothing has been registered yet, asking would only ever return nothing and a
		// server is entitled to treat an empty kind list as a mistake
		return nil
	}
	var set RunnerSet
	ctx, cf := context.WithTimeout(dcm.ctx, callTimeout)
	defer cf()
	if err = sess.Call(ctx, MethodListRunners, q, &set); err != nil {
		return
	}
	return dcm.Sync(set.Runners)
}

// Sync reconciles the remote set of configured runners with what is on disk.
//
// The comparison is done on the rendered INI rather than on the definitions themselves.
// That is deliberate: a definition makes a round trip through JSON on the way here, so an
// int arrives as a float and a []string as a []any, and comparing the decoded values
// would report a change on every poll.  The INI is what actually lands on disk, so
// comparing it answers the only question that matters, would the file be different.
//
// Runners this ingester registered itself are left alone.  The server does not know about
// them, and letting its answer delete them would take out the ingester's own static
// configuration.
func (dcm *DynamicConfigManager) Sync(remote []RunnerDefinition) (err error) {
	dcm.mtx.Lock()
	defer dcm.mtx.Unlock()

	// render everything the server sent, rejecting anything we cannot write, before
	// touching the directory
	type incoming struct {
		rd  RunnerDefinition
		ini string
	}
	wanted := make(map[uuid.UUID]incoming, len(remote))
	for _, rd := range remote {
		if rd.UUID == uuid.Nil() {
			dcm.lgr.Warn("dynamic config skipping a runner with no UUID",
				log.KV("kind", rd.Kind), log.KV("name", rd.Name))
			continue
		}
		if _, ok := dcm.lookupKindLocked(rd.Kind); !ok {
			dcm.lgr.Warn("dynamic config skipping a runner of an unsupported kind",
				log.KV("kind", rd.Kind), log.KV("name", rd.Name))
			continue
		}
		var blob string
		if blob, err = rd.INI(); err != nil {
			dcm.lgr.Error("dynamic config skipping a runner that cannot be written",
				log.KV("kind", rd.Kind), log.KV("name", rd.Name), log.KVErr(err))
			continue
		}
		wanted[rd.UUID] = incoming{rd: rd, ini: blob}
	}

	var added, updated, removed int
	kept := make([]configuredRunner, 0, len(dcm.Configured))

	for _, cur := range dcm.Configured {
		if !cur.remote {
			kept = append(kept, cur) // ours, not the server's to take away
			continue
		}
		want, still := wanted[cur.UUID]
		if !still {
			// deleted upstream
			if cur.backingFile != `` {
				if rerr := os.Remove(cur.backingFile); rerr != nil && !os.IsNotExist(rerr) {
					dcm.lgr.Error("dynamic config failed to remove a config",
						log.KV("file", cur.backingFile), log.KVErr(rerr))
				}
			}
			removed++
			continue
		}
		delete(wanted, cur.UUID)

		existing, rerr := os.ReadFile(cur.backingFile)
		if rerr == nil && string(existing) == want.ini {
			kept = append(kept, cur) // unchanged, leave the file alone
			continue
		}
		pth := dcm.runnerPath(want.rd)
		if err = writeConfFile(pth, want.ini); err != nil {
			return fmt.Errorf("failed to write %s %w", pth, err)
		}
		// the name may have changed, which changes the file name, so drop the old one
		if cur.backingFile != `` && cur.backingFile != pth {
			os.Remove(cur.backingFile)
		}
		kept = append(kept, configuredRunner{RunnerDefinition: want.rd, backingFile: pth, remote: true})
		updated++
	}

	// whatever is left in wanted is new
	for _, want := range wanted {
		pth := dcm.runnerPath(want.rd)
		if err = writeConfFile(pth, want.ini); err != nil {
			return fmt.Errorf("failed to write %s %w", pth, err)
		}
		kept = append(kept, configuredRunner{RunnerDefinition: want.rd, backingFile: pth, remote: true})
		added++
	}

	if added == 0 && updated == 0 && removed == 0 {
		return nil // nothing moved, do not wake the ingester
	}
	dcm.Configured = kept
	dcm.lgr.Info("dynamic config changed", log.KV("added", added),
		log.KV("updated", updated), log.KV("removed", removed))
	dcm.bump()
	return nil
}

// runnerPath is where a runner's config lives, kind_name_uuid.conf in the storage
// directory.  It is derived rather than remembered so that the same runner always lands
// in the same place.
func (dcm *DynamicConfigManager) runnerPath(rd RunnerDefinition) string {
	return filepath.Join(dcm.Storage,
		fmt.Sprintf("%s_%s_%v.conf", fnameChunk(rd.Kind), fnameChunk(rd.Name), rd.UUID))
}

// bump signals the main loop that the configuration on disk has changed.
//
// The channel is buffered by one and the send is non blocking, so a burst of changes
// collapses into a single reload rather than queueing one per change, and a caller that
// is not listening never blocks the sync.
func (dcm *DynamicConfigManager) bump() {
	select {
	case dcm.ch <- struct{}{}:
	default:
	}
}

// applyConfig is what a webserver calls to push a single configuration without waiting
// for the next poll.  It is a hint, not a replacement for the poll: the runner is merged
// into the current set and the full reconciliation still happens on the next refresh.
func (dcm *DynamicConfigManager) applyConfig(_ context.Context, params json.RawMessage) (any, error) {
	var rd RunnerDefinition
	if err := json.Unmarshal(params, &rd); err != nil {
		return nil, fmt.Errorf("bad configuration %w", err)
	}
	if rd.UUID == uuid.Nil() {
		return nil, errors.New("configuration has no UUID")
	}
	// fold it into the set we already hold so that a push cannot delete anything, only
	// add or change
	current := dcm.remoteRunners()
	replaced := false
	for i := range current {
		if current[i].UUID == rd.UUID {
			current[i] = rd
			replaced = true
			break
		}
	}
	if !replaced {
		current = append(current, rd)
	}
	if err := dcm.Sync(current); err != nil {
		return nil, err
	}
	return map[string]any{`ok`: true}, nil
}

// remoteRunners copies the definitions we currently hold from the server.
func (dcm *DynamicConfigManager) remoteRunners() (r []RunnerDefinition) {
	dcm.mtx.Lock()
	defer dcm.mtx.Unlock()
	for _, cur := range dcm.Configured {
		if cur.remote {
			r = append(r, cur.RunnerDefinition)
		}
	}
	return
}
