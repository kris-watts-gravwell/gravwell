/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"testing"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/server"
)

// TestStoreConformance runs the interface's own suite against the SQLite backing.  It is
// what keeps this store and the webserver's from drifting: a rule either backend gets
// wrong is caught here rather than six months later on somebody's status page.
func TestStoreConformance(t *testing.T) {
	server.TestStore(t, func(_ *testing.T) server.Store {
		// a fresh store per case, the suite requires each one to be independent of the
		// last and several of these check what a store holds in total
		return NewStore()
	})
}
