/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), `status.db`))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// only returns the single status held for a runner, failing if there is not exactly one.
func only(t *testing.T, s *Store, runner uuid.UUID) StatusRow {
	t.Helper()
	rows, err := s.RunnerStatuses(runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("runner %v has %d statuses, want 1", runner, len(rows))
	}
	return rows[0]
}

// TestReplaceStatuses covers the replace semantics the whole design leans on: a report is
// the complete truth for one ingester, so clearing needs no retraction, and one ingester
// can never speak for another.
func TestReplaceStatuses(t *testing.T) {
	s := newStore(t)
	inA, inB := uuid.New(), uuid.New()
	r1, r2 := uuid.New(), uuid.New()

	// A reports one failing runner and one clean one
	if err := s.ReplaceStatuses(inA, []dynamic.RunnerStatus{
		{UUID: r1, Kind: `Tester`, Name: `beat`, Error: `missing unit in duration "3"`},
		{UUID: r2, Kind: `Tester`, Name: `other`},
	}); err != nil {
		t.Fatal(err)
	}
	bad := only(t, s, r1)
	if bad.OK() || bad.Ingester != inA || bad.Name != `beat` {
		t.Fatalf("bad status not recorded: %+v", bad)
	}
	if only(t, s, r2).OK() != true {
		t.Error(`a clean runner should be recorded as clean, not left absent`)
	}
	firstSeen := bad.Since

	// the same failure again keeps its clock running, so a row can answer "for how long"
	if err := s.ReplaceStatuses(inA, []dynamic.RunnerStatus{
		{UUID: r1, Kind: `Tester`, Name: `beat`, Error: `missing unit in duration "3"`},
		{UUID: r2, Kind: `Tester`, Name: `other`},
	}); err != nil {
		t.Fatal(err)
	}
	again := only(t, s, r1)
	if !again.Since.Equal(firstSeen) {
		t.Errorf("an unchanged failure restarted its clock: %v then %v", firstSeen, again.Since)
	}
	if !again.Updated.After(firstSeen) && again.Updated.Before(firstSeen) {
		t.Error(`the report time should move even when the state does not`)
	}

	// a different message is a different state and does start the clock again
	if err := s.ReplaceStatuses(inA, []dynamic.RunnerStatus{
		{UUID: r1, Kind: `Tester`, Name: `beat`, Error: `something else entirely`},
	}); err != nil {
		t.Fatal(err)
	}
	if changed := only(t, s, r1); changed.Since.Equal(firstSeen) {
		t.Error(`a different failure should start the clock again`)
	}

	// r2 was not in that report, so A has stopped carrying it and its row is gone
	if rows, err := s.RunnerStatuses(r2); err != nil {
		t.Fatal(err)
	} else if len(rows) != 0 {
		t.Errorf("a runner left out of a report kept %d rows", len(rows))
	}

	// B reporting clean does not touch what A said, they are separate opinions
	if err := s.ReplaceStatuses(inB, []dynamic.RunnerStatus{{UUID: r1, Kind: `Tester`, Name: `beat`}}); err != nil {
		t.Fatal(err)
	}
	rows, err := s.RunnerStatuses(r1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("two ingesters reported, got %d rows", len(rows))
	}
	// the failing one sorts first, an operator opened the page to read that
	if rows[0].OK() || rows[0].Ingester != inA {
		t.Errorf("the failure should sort first, got %+v", rows[0])
	}
	if !rows[1].OK() || rows[1].Ingester != inB {
		t.Errorf("the clean report is wrong: %+v", rows[1])
	}

	// A comes good, and the error clears with nothing having to retract it
	if err := s.ReplaceStatuses(inA, []dynamic.RunnerStatus{{UUID: r1, Kind: `Tester`, Name: `beat`}}); err != nil {
		t.Fatal(err)
	}
	if rows, err = s.RunnerStatuses(r1); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if !row.OK() {
			t.Errorf("an error survived a clean report: %+v", row)
		}
	}

	// an ingester with nothing to say clears itself entirely
	if err := s.ReplaceStatuses(inA, nil); err != nil {
		t.Fatal(err)
	}
	if rows, err = s.RunnerStatuses(r1); err != nil {
		t.Fatal(err)
	} else if len(rows) != 1 || rows[0].Ingester != inB {
		t.Errorf("an empty report should drop only its own rows, got %+v", rows)
	}

	// a report from nobody is refused rather than filed under the nil UUID
	if err = s.ReplaceStatuses(uuid.Nil(), nil); err == nil {
		t.Error(`a status report with no ingester UUID should be refused`)
	}
}

// TestDeleteRunnerClearsStatuses covers the one case a report can never clean up after:
// once the runner is gone no ingester will ever mention it again, so a stale error would
// sit on the screen forever.
func TestDeleteRunnerClearsStatuses(t *testing.T) {
	s := newStore(t)
	ingester, runner := uuid.New(), uuid.New()
	rd, err := dynamic.MapRunnerDefinition(`Tester`, `beat`, struct {
		Ingester_UUID string
		Interval      string
	}{Ingester_UUID: runner.String(), Interval: `3`})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.PutRunner(rd); err != nil {
		t.Fatal(err)
	}
	if err = s.ReplaceStatuses(ingester, []dynamic.RunnerStatus{
		{UUID: runner, Kind: `Tester`, Name: `beat`, Error: `boom`},
	}); err != nil {
		t.Fatal(err)
	}
	if rows, _ := s.RunnerStatuses(runner); len(rows) != 1 {
		t.Fatalf("setup failed, got %d rows", len(rows))
	}
	if err = s.DeleteRunner(runner); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.RunnerStatuses(runner); err != nil {
		t.Fatal(err)
	} else if len(rows) != 0 {
		t.Errorf("deleting a runner left %d statuses behind", len(rows))
	}
}

// TestRollUp covers the reduction the list draws from.  One ingester failing is the whole
// runner failing, and never having been reported on is not the same as being fine.
func TestRollUp(t *testing.T) {
	ok := StatusRow{}
	bad := StatusRow{Error: `missing unit in duration "3"`}

	if state, detail := rollUp(nil); state != stateUnknown || detail == `` {
		t.Errorf("no reports rolled up to %q %q", state, detail)
	}
	if state, _ := rollUp([]StatusRow{ok, ok}); state != stateOK {
		t.Errorf("two clean reports rolled up to %q", state)
	}
	state, detail := rollUp([]StatusRow{bad})
	if state != stateBad || detail != bad.Error {
		t.Errorf("a single failure rolled up to %q %q, want the plugin's own words", state, detail)
	}
	// the mixed case is the one that matters, a green light here would be a lie
	if state, detail = rollUp([]StatusRow{ok, bad, ok}); state != stateBad {
		t.Errorf("one failure among three rolled up to %q, want %q", state, stateBad)
	} else if !strings.Contains(detail, `1 of 3 ingesters`) || !strings.Contains(detail, bad.Error) {
		t.Errorf("the mixed summary does not say who or why: %q", detail)
	}
}
