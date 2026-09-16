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
	"fmt"
	"net/http"
	"time"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/rpc"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

// authRateWindow is how often one address may attempt to authenticate.  It is a variable
// rather than a constant so the tests can turn it off: they stand several ingesters up
// back to back from 127.0.0.1, which a throttle is supposed to refuse.
var authRateWindow = 100 * time.Millisecond

// NewServer wires the RPC route and the web interface onto one handler.  It is built
// separately from main so that the tests can stand the whole thing up in process.
func NewServer(store *Store, secret string, lgr *log.Logger) (h http.Handler, err error) {
	if lgr == nil {
		lgr = log.NewDiscardLogger()
	}
	api := NewAPI(store, lgr)

	var mux *rpc.Mux
	if mux, err = api.Mux(); err != nil {
		return nil, fmt.Errorf("failed to build the RPC method set %w", err)
	}
	var rsrv *rpc.Server
	if rsrv, err = rpc.NewServer(rpc.ServerConfig{
		Token:     secret,
		Handlers:  mux,
		OnSession: api.OnSession,
		Logger:    lgr,
		// a development rig gets reconnected at constantly while something is being
		// worked on, so the throttle is loosened rather than left at the production
		// default of one attempt per second
		AuthRateWindow: authRateWindow,
		// a test server usually sits behind nothing, but it is common enough to run one
		// behind a tunnel or a reverse proxy on a dev box
		TrustedProxies: []string{`127.0.0.0/8`, `::1`},
	}); err != nil {
		return nil, fmt.Errorf("failed to build the RPC server %w", err)
	}

	ui, err := NewUI(api, store, lgr)
	if err != nil {
		return nil, err
	}

	smux := http.NewServeMux()
	smux.Handle(RPCPath, rsrv)
	ui.Register(smux)
	return smux, nil
}

// contextWithTimeout is a tiny helper so main does not have to import context.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
