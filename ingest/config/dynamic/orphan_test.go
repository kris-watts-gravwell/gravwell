/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"uuid"
)

// syncDef builds a definition the webserver would hand down for the mock kind.
func syncDef(t *testing.T, name string, id uuid.UUID, tag string) RunnerDefinition {
	t.Helper()
	rd, err := MapRunnerDefinition(`mock`, name, mockRunner{Tag_Name: tag})
	if err != nil {
		t.Fatal(err)
	}
	rd.UUID = id
	return rd
}

// confNames lists the config files in a directory.
func confNames(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var r []string
	for _, e := range ents {
		if filepath.Ext(e.Name()) == confExt {
			r = append(r, e.Name())
		}
	}
	return r
}

// TestSyncSweepsConfigsDeletedWhileDown is the restart case.
//
// The in memory list of configured runners is empty every time the process starts, so a
// configuration the server deleted while this ingester was down is remembered by nothing
// and was previously left on disk forever, loaded on every start.
func TestSyncSweepsConfigsDeletedWhileDown(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}

	stays, goes := uuid.New(), uuid.New()
	if err := dcm.Sync([]RunnerDefinition{
		syncDef(t, `stays`, stays, `a`),
		syncDef(t, `goes`, goes, `b`),
	}); err != nil {
		t.Fatal(err)
	}
	if n := len(confNames(t, dir)); n != 2 {
		t.Fatalf("setup wrote %d configs, want 2", n)
	}

	// the restart: everything in memory is gone, the files are not
	dcm.Configured = nil

	// the server now only has one of them
	if err := dcm.Sync([]RunnerDefinition{syncDef(t, `stays`, stays, `a`)}); err != nil {
		t.Fatal(err)
	}
	names := confNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("after the sweep the directory holds %v, want just the surviving runner", names)
	}
	if !strings.Contains(names[0], stays.String()) {
		t.Errorf("the wrong config survived: %v", names)
	}
}

// TestSyncSweepsRenamedConfigs covers the rename, which leaves two files for one runner:
// the file name carries the name, so a rename writes a new file and the old one is
// orphaned under a UUID that is still live.
func TestSyncSweepsRenamedConfigs(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}

	id := uuid.New()
	if err := dcm.Sync([]RunnerDefinition{syncDef(t, `before`, id, `a`)}); err != nil {
		t.Fatal(err)
	}
	dcm.Configured = nil // the restart

	if err := dcm.Sync([]RunnerDefinition{syncDef(t, `after`, id, `a`)}); err != nil {
		t.Fatal(err)
	}
	names := confNames(t, dir)
	if len(names) != 1 {
		t.Fatalf("a rename left %v, want one file", names)
	}
	if !strings.Contains(names[0], `after`) {
		t.Errorf("the stale name survived: %v", names)
	}
}

// TestSyncLeavesLocalConfigsAlone is the guard rail on the sweep.  A configuration the
// ingester registered for itself carries no marker, and the server has no standing to
// delete it however little it knows about it.
func TestSyncLeavesLocalConfigsAlone(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}
	// the ingester's own runner, written by RegisterRunner
	if err := dcm.RegisterRunner(`local`, `mock`, uuid.New(), mockRunner{Tag_Name: `x`}); err != nil {
		t.Fatal(err)
	}
	before := confNames(t, dir)
	if len(before) != 1 {
		t.Fatalf("setup wrote %v", before)
	}

	dcm.Configured = nil // the restart, so even the local runner is forgotten

	// the server sends nothing at all
	if err := dcm.Sync(nil); err != nil {
		t.Fatal(err)
	}
	after := confNames(t, dir)
	if len(after) != 1 || after[0] != before[0] {
		t.Errorf("the ingester's own configuration was swept: had %v, now %v", before, after)
	}
}

// TestSyncLeavesUnmarkedFilesAlone covers anything else in the directory: a file dropped
// in by hand is not the server's to remove.
func TestSyncLeavesUnmarkedFilesAlone(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}
	hand := filepath.Join(dir, `handwritten`+confExt)
	if err := os.WriteFile(hand, []byte("[mock \"hand\"]\n\tTag-Name=`x`\n"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := dcm.Sync(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hand); err != nil {
		t.Errorf("a hand written config was swept: %v", err)
	}
}

// TestSyncKeepsTheLastGoodCopyOfARejectedRunner checks the sweep does not undo the thing
// the reconcile went out of its way to do: hold on to a working file when the server
// sends a version that will not run.
func TestSyncKeepsTheLastGoodCopyOfARejectedRunner(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if err := dcm.Sync([]RunnerDefinition{syncDef(t, `beat`, id, `good`)}); err != nil {
		t.Fatal(err)
	}
	good := confNames(t, dir)
	if len(good) != 1 {
		t.Fatalf("setup wrote %v", good)
	}

	// a definition carrying a value that cannot be written into a config file
	bad := syncDef(t, `beat`, id, "tag\x01with\\a control char")
	if err := dcm.Sync([]RunnerDefinition{bad}); err != nil {
		t.Fatal(err)
	}
	after := confNames(t, dir)
	if len(after) != 1 || after[0] != good[0] {
		t.Errorf("the working copy was swept when the server sent a bad one: had %v, now %v", good, after)
	}
}

// TestRemoteMarkerIsInvisibleToTheLoader checks the marker does not change what a config
// means, only who owns it.
func TestRemoteMarkerIsInvisibleToTheLoader(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`mock`, false, mockRunner{}); err != nil {
		t.Fatal(err)
	}
	if err := dcm.Sync([]RunnerDefinition{syncDef(t, `beat`, uuid.New(), `thetag`)}); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Mock map[string]*mockRunner
	}
	if err := dcm.Load(&cfg); err != nil {
		t.Fatalf("a marked config would not load: %v", err)
	}
	if cfg.Mock[`beat`] == nil {
		t.Fatal(`the marked config did not load`)
	}
	if cfg.Mock[`beat`].Tag_Name != `thetag` {
		t.Errorf("the marker changed what loaded: %+v", cfg.Mock[`beat`])
	}
	if n := len(dcm.Statuses()); n > 0 {
		for _, rs := range dcm.Statuses() {
			if !rs.OK() {
				t.Errorf("a marked config was reported as failing to load: %+v", rs)
			}
		}
	}
}
