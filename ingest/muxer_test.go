/*************************************************************************
 * Copyright 2017 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package ingest

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// rawStr builds a valid JSON string value of n filler bytes for use as a
// Configuration/Metadata block.
func rawStr(n int) json.RawMessage {
	return json.RawMessage(`"` + strings.Repeat("C", n) + `"`)
}

// makeChildren builds n child states, each carrying a Name of nameBytes and,
// when cfgBytes > 0, a Configuration block of cfgBytes.
func makeChildren(n, nameBytes, cfgBytes int) map[string]IngesterState {
	m := make(map[string]IngesterState, n)
	name := strings.Repeat("A", nameBytes)
	var cfg json.RawMessage
	if cfgBytes > 0 {
		cfg = rawStr(cfgBytes)
	}
	for i := 0; i < n; i++ {
		key := fmt.Sprintf("child-%04d", i)
		m[key] = IngesterState{
			UUID:          key,
			Name:          name,
			Configuration: cfg,
		}
	}
	return m
}

// muxerForState returns a bare muxer wired up with just enough state to exercise
// getTrimmedState. No logger is set; im.Error is a no-op when lgr is nil.
func muxerForState(s IngesterState) *IngestMuxer {
	return &IngestMuxer{
		mtx:           &sync.RWMutex{},
		start:         time.Now(),
		ingesterState: s,
	}
}

func assertFits(t *testing.T, s IngesterState) {
	t.Helper()
	sz, err := s.EncodedSize()
	if err != nil {
		t.Fatalf("EncodedSize failed: %v", err)
	}
	if sz > maxIngestStateSize {
		t.Fatalf("state does not fit after trimming: %d > %d", sz, maxIngestStateSize)
	}
}

// TestGetTrimmedStateStages walks each stage of the progressive size reduction
// in getTrimmedState.  Each subtest is built so that the state only drops below
// maxIngestStateSize at the specific stage under test, proving every earlier
// stage ran but was insufficient and that the target stage is what made it fit.
func TestGetTrimmedStateStages(t *testing.T) {
	const MB = int(maxIngestStateSize)

	run := func(s IngesterState) (IngesterState, bool, error) {
		return muxerForState(s).getTrimmedState(time.Time{}, 0)
	}

	// stage 0: already fits, nothing is trimmed
	t.Run("fits-no-trim", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Name:          "small",
			Configuration: rawStr(1024),
			Metadata:      rawStr(1024),
			Children:      makeChildren(2, 512, 512),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if out.Configuration == nil || out.Metadata == nil {
			t.Fatal("stage0: reporting blocks should be retained when the state already fits")
		}
		if len(out.Children) != 2 {
			t.Fatalf("stage0: children should be untouched, got %d", len(out.Children))
		}
		assertFits(t, out)
	})

	// stage 1: child configuration/metadata blocks are what push us over
	t.Run("stage1-child-configs", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Configuration: rawStr(1024), // small own config that must survive
			Children:      makeChildren(4, 256, 400*1024),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if len(out.Children) != 4 {
			t.Fatalf("stage1: children should be retained, got %d", len(out.Children))
		}
		for k, c := range out.Children {
			if c.Configuration != nil {
				t.Fatalf("stage1: child %s config was not trimmed", k)
			}
		}
		if out.Configuration == nil {
			t.Fatal("stage1: own configuration should still be present")
		}
		assertFits(t, out)
	})

	// stage 2: our own configuration/metadata blocks dominate
	t.Run("stage2-own-config", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Configuration: rawStr(2 * MB),
			Metadata:      rawStr(1024),
			Children:      makeChildren(2, 256, 1024),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if out.Configuration != nil || out.Metadata != nil {
			t.Fatal("stage2: own configuration/metadata should be dropped")
		}
		if len(out.Children) != 2 {
			t.Fatalf("stage2: children should be retained, got %d", len(out.Children))
		}
		assertFits(t, out)
	})

	// stage 3: trimChildren(64) - too many children, sized so 64 fit
	t.Run("stage3-trim-children-64", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Children: makeChildren(100, 12*1024, 0),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if len(out.Children) != 64 {
			t.Fatalf("stage3: expected 64 children after trimChildren(64), got %d", len(out.Children))
		}
		assertFits(t, out)
	})

	// stage 4: trimChildren(8) - 64 children still too big, 8 fit
	t.Run("stage4-trim-children-8", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Children: makeChildren(100, 30*1024, 0),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if len(out.Children) != 8 {
			t.Fatalf("stage4: expected 8 children after trimChildren(8), got %d", len(out.Children))
		}
		assertFits(t, out)
	})

	// stage 5: even 7 children are too big, drop them entirely
	t.Run("stage5-drop-children", func(t *testing.T) {
		out, push, err := run(IngesterState{
			Children: makeChildren(100, 200*1024, 0),
		})
		if err != nil || !push {
			t.Fatalf("unexpected err=%v push=%v", err, push)
		}
		if len(out.Children) != 0 {
			t.Fatalf("stage5: expected all children dropped, got %d", len(out.Children))
		}
		assertFits(t, out)
	})

	// stage 6: nothing trimmable can shrink it (Name is never trimmed) -> error
	t.Run("stage6-unshrinkable-errors", func(t *testing.T) {
		_, push, err := run(IngesterState{
			Name: strings.Repeat("A", 2*MB),
		})
		if !push {
			t.Fatal("stage6: expected shouldPush true")
		}
		if err == nil {
			t.Fatal("stage6: expected an error when the state cannot be shrunk below the limit")
		}
	})
}

// TestCloseWithStalledIndexer reproduces issue 2923 at the muxer level.  The
// indexer authenticates, keeps its keepalive flowing, and then never answers
// the first post-auth setup command.  WaitForHot is expected to time out, and
// Close must still return: the connection routine is parked in the setup
// exchange and nothing is in im.igst yet, so the only way out is for the
// exchange itself to be bounded and to honor the muxer context.
//
// This runs with the real timeouts on purpose: Close has to get out through
// context cancellation, well before the command budget expires.
func TestCloseWithStalledIndexer(t *testing.T) {
	// real wall clock bounds for this test, the indexer is a real socket so
	// nothing here runs on a fake clock
	const (
		attachDeadline = 10 * time.Second
		closeDeadline  = 15 * time.Second
	)

	ti, err := newTestIndexer(syncTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	defer ti.Close()
	ti.stallAfterAuth()

	mxr, err := NewUniformMuxer(UniformMuxerConfig{
		Destinations:    []string{ti.Destination()},
		Tags:            []string{syncTestTag},
		Auth:            syncTestSecret,
		CacheDepth:      syncTestDepth,
		IngesterName:    `closetest`,
		IngesterVersion: `test`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = mxr.Start(); err != nil {
		t.Fatal(err)
	}

	//make sure we are actually attached and stalled rather than failing to connect at all
	if err = waitForConns(ti, 1, attachDeadline); err != nil {
		t.Fatal(err)
	}
	//the indexer is wedged, so we must not go hot
	if err = mxr.WaitForHot(time.Second); err == nil {
		t.Fatal("muxer went hot against an indexer that never finished setup")
	}

	//Close used to block forever here on im.wg.Wait()
	start := time.Now()
	errCh := make(chan error, 1)
	go func() { errCh <- mxr.Close() }()
	select {
	case err = <-errCh:
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}
	case <-time.After(closeDeadline):
		t.Fatalf("Close did not return within %v with a stalled indexer", closeDeadline)
	}
	t.Logf("Close returned in %v", time.Since(start))

	//and the half set up connection must actually have been torn down, not leaked
	if err = waitForConns(ti, 0, closeDeadline); err != nil {
		t.Fatal(err)
	}
}

// waitForConns polls the indexer until it has exactly want connections
// attached, or the deadline passes.
func waitForConns(ti *testIndexer, want int64, to time.Duration) error {
	deadline := time.Now().Add(to)
	for {
		if n := ti.Conns(); n == want {
			return nil
		} else if time.Now().After(deadline) {
			return fmt.Errorf("expected %d connections, have %d after %v", want, n, to)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
