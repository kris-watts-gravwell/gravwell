/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import "uuid"

// The methods spoken between an ingester and a webserver over an authenticated RPC
// session.  They live here rather than in the rpc package because rpc moves bytes and
// knows nothing about configuration, and they live here rather than in either endpoint
// because both ends have to agree on them.
// RPCPath is where a webserver serves the ingester facing websocket.  It is part of the
// protocol rather than a deployment detail, both ends have to agree on it.
const RPCPath = `/api/ingester/hosted`

const (
	// MethodRegisterKinds is called by an ingester once per connection to declare
	// everything it is able to run.  It sends the complete set, so the server replaces
	// whatever it held for that ingester rather than merging, which is what lets a kind
	// that has been removed from a build actually disappear.
	MethodRegisterKinds = `registerKinds`

	// MethodListRunners is called by an ingester to ask which configured runners it
	// should be running.
	MethodListRunners = `listRunners`

	// MethodApplyConfig is called by a webserver down to an ingester to push a single
	// configuration without waiting for the next poll.  An ingester that does not
	// implement it simply gets its changes on the next poll instead.
	MethodApplyConfig = `applyConfig`
)

// RegisterKindsRequest declares what an ingester can run.
//
// The identity is carried in the body as well as being bound into the session handshake.
// A server must trust the session, not the body: the handshake proves who the peer is and
// this is only a convenience for logging and for servers that route on class.
type RegisterKindsRequest struct {
	ID    uuid.UUID
	Class string             `json:",omitempty"`
	Kinds []RunnerDefinition `json:",omitempty"`
}

// RunnerQuery asks for the configurations an ingester should be running.
//
// The three filters are how a webserver decides what belongs to whom: Kinds is what this
// build can actually run, and ID and Class are what an Assignment can point at.  A server
// returns a runner when its kind is supported here and its assignment names this ingester,
// names this class, or names nobody at all.
type RunnerQuery struct {
	ID    uuid.UUID
	Class string   `json:",omitempty"`
	Kinds []string `json:",omitempty"`
}

// Matches reports whether a runner should be handed to the ingester that sent this query.
// It lives here so that both ends agree on the rule rather than each inventing one.
//
// A runner has to be of a kind this ingester registered, and it has to pass every filter
// its assignment sets.  A list that is present excludes everything not in it, which is
// the point: a configuration pinned to three UUIDs must never reach a fourth ingester.
// A list that is absent is not a filter at all.
func (q RunnerQuery) Matches(rd RunnerDefinition) bool {
	if !q.supports(rd.Kind) {
		return false
	}
	if rd.Assigned.Empty() {
		return true // unassigned, anything that can run it may have it
	}
	if !rd.Assigned.AllowsUUID(q.ID) {
		return false
	}
	if !rd.Assigned.AllowsClass(q.Class) {
		return false
	}
	// a group is something this protocol has no way to evaluate yet, so a runner pinned
	// to one is deliberately handed to nobody rather than to everybody
	return rd.Assigned.Group == ``
}

func (q RunnerQuery) supports(kind string) bool {
	for _, k := range q.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// RunnerSet is the answer to a RunnerQuery.
type RunnerSet struct {
	Runners []RunnerDefinition `json:",omitempty"`
}
