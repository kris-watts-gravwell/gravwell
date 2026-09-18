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
	"strings"
	"testing"
	"time"

	"uuid"

	"github.com/gravwell/gravwell/v4/client"
	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/rpc"
)

// slowIngester connects and answers applyConfig after a delay, which is how an ingester
// on a busy box or a slow disk behaves.
func slowIngester(t *testing.T, h *harness, delay time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mux := rpc.NewMux()
	if err := mux.Register(dynamic.MethodApplyConfig, func(context.Context, json.RawMessage) (any, error) {
		time.Sleep(delay)
		return map[string]any{`ok`: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	ctx, cf := context.WithTimeout(context.Background(), 10*time.Second)
	defer cf()
	s, err := rpc.Dial(ctx, rpc.ClientConfig{
		Webserver: h.ts.URL, Path: client.INGESTERS_CONTROL_URL, Token: testSecret,
		ID: id, Class: `edge`, Handlers: mux, PingInterval: -1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// the server has to know what this one can run or the push will not target it
	if err = s.Call(ctx, dynamic.MethodRegisterKinds, dynamic.RegisterKindsRequest{
		ID: id, Class: `edge`, Kinds: []dynamic.RunnerDefinition{proto(t)},
	}, nil); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestPushGivesEachIngesterItsOwnDeadline is the regression guard.
//
// One context built outside the loop meant the timeout was a budget spent across every
// ingester rather than a bound applied to each: with enough of them, or one slow one, the
// ingesters at the back were reported as having refused a configuration they were never
// actually asked about.
func TestPushGivesEachIngesterItsOwnDeadline(t *testing.T) {
	h := newHarness(t)
	// several ingesters, each slow enough that in total they would blow a shared budget
	// many times over.  pushTimeout is 10s, so five of these share it only if the loop is
	// sequential on one context.
	const each = 3 * time.Second
	const count = 5
	for i := 0; i < count; i++ {
		slowIngester(t, h, each)
	}
	waitFor(t, `every ingester to be connected`, func() bool {
		_, body := h.get(t, `/ui/status`)
		return strings.Contains(body, `5 ingesters connected`)
	})

	rd := proto(t)
	rd.Name, rd.UUID = `prod`, uuid.New()

	start := time.Now()
	delivered, errs := h.api().Push(rd)
	elapsed := time.Since(start)

	if len(errs) != 0 {
		t.Errorf("healthy ingesters were reported as rejecting the push: %v", errs)
	}
	if delivered != count {
		t.Errorf("delivered to %d of %d ingesters", delivered, count)
	}
	// running them together, so the whole push takes about as long as the slowest one
	// rather than the sum.  Sequentially this would be 15s.
	if elapsed > each*2 {
		t.Errorf("push took %v for %d ingesters at %v each, they were queued behind each other", elapsed, count, each)
	}
}
