/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

// Command test_server is a stand in webserver for developing and testing the dynamic
// ingester configuration system.  It speaks the server half of the dynamic config RPC
// protocol, stores what ingesters report in SQLite, and serves a small web interface for
// creating and editing runner configurations.
//
// It is a development tool, not a product.  It has no user authentication on the web
// interface, so bind it to a loopback address or a trusted network.
//
//	test_server -bind 127.0.0.1:8080 -secret <shared token> -storage ./dynamic.db
//
// Ingesters connect to ws://<bind>/api/ingester/hosted and authenticate with the same
// shared token, which is never transmitted, see the rpc package for how that works.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

// RPCPath is where the ingester facing websocket lives.  It comes from the dynamic
// package so that the ingester and this server cannot disagree about it.
const RPCPath = dynamic.RPCPath

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 5 * time.Second
)

var (
	bind    = flag.String(`bind`, `127.0.0.1:8080`, "address:port to serve the HTTP interface on")
	secret  = flag.String(`secret`, ``, "shared authentication token, required")
	storage = flag.String(`storage`, ``, "path to the SQLite database holding runner definitions, required")
)

func main() {
	flag.Parse()
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func run() (err error) {
	if *secret == `` {
		return errors.New("-secret is required, it is the shared token ingesters authenticate with")
	} else if *storage == `` {
		return errors.New("-storage is required, it is the SQLite database to keep definitions in")
	}

	lgr, err := log.NewStderrLogger(``)
	if err != nil {
		return fmt.Errorf("failed to build a logger %w", err)
	}
	defer lgr.Close()

	store, err := OpenStore(*storage)
	if err != nil {
		return err
	}
	defer store.Close()

	srv, err := NewServer(store, *secret, lgr)
	if err != nil {
		return err
	}

	hsrv := &http.Server{
		Addr:              *bind,
		Handler:           srv,
		ReadHeaderTimeout: readHeaderTimeout,
	}

	// serve in the background so that a signal can shut it down cleanly, an interrupted
	// test server should not leave a half written database behind
	errCh := make(chan error, 1)
	go func() {
		lgr.Info("serving", log.KV("bind", *bind), log.KV("rpc", RPCPath),
			log.KV("storage", *storage))
		fmt.Printf("dynamic config test server\n  web interface  http://%s/\n  ingester RPC   ws://%s%s\n  storage        %s\n",
			*bind, *bind, RPCPath, *storage)
		if lerr := hsrv.ListenAndServe(); lerr != nil && !errors.Is(lerr, http.ErrServerClosed) {
			errCh <- lerr
		}
		close(errCh)
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err = <-errCh:
		if err != nil {
			return fmt.Errorf("failed to serve %w", err)
		}
	case s := <-sig:
		lgr.Info("shutting down", log.KV("signal", s.String()))
	}

	ctx, cf := contextWithTimeout(shutdownTimeout)
	defer cf()
	if err = hsrv.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shut down cleanly %w", err)
	}
	return nil
}
