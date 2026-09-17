/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"errors"
	"strings"
	"testing"

	"uuid"
)

// nestedSpec is a nested member, the shape no shipped plugin has yet and the one the INI
// writer cannot represent.
type nestedSpec struct {
	Field string
	Value string
}

// optionalNestedCfg declares a nested member without requiring one, which is the ordinary
// way such a thing turns up in a plugin config.
type optionalNestedCfg struct {
	Ingester_UUID string
	Tag_Name      string
	Filters       []nestedSpec
}

// TestINIAcceptsAnUnsetNestedMember is the regression guard on refusing too much.
//
// A nested member that cannot be written is only a problem when something was put in it.
// Refusing on the declared type instead made every configuration of a plugin that merely
// declares one unwritable, so the plugin could not be deployed at all: Sync rejects a
// definition it cannot render, so nothing of that kind would ever reach disk.
func TestINIAcceptsAnUnsetNestedMember(t *testing.T) {
	rd, err := MapRunnerDefinition(`Nested`, `beat`, optionalNestedCfg{
		Ingester_UUID: uuid.New().String(),
		Tag_Name:      `test`,
		// Filters left alone
	})
	if err != nil {
		t.Fatal(err)
	}
	// the variable is still described, it just carries no value
	v, ok := findVar(rd, `Filters`)
	if !ok {
		t.Fatal(`the nested member should still be described`)
	}
	if v.Value != nil {
		t.Fatalf("setup is wrong, the member carries a value: %+v", v)
	}

	ini, err := rd.INI()
	if err != nil {
		t.Fatalf("a configuration whose nested member is unset was refused: %v", err)
	}
	if !strings.Contains(ini, "Tag-Name") {
		t.Errorf("the rest of the configuration was not written:\n%s", ini)
	}
	if strings.Contains(ini, `Filters`) {
		t.Errorf("an unset member should not be emitted:\n%s", ini)
	}
}

// TestINIRefusesAPopulatedNestedMember is the other half: once something is in it, the
// block cannot be written and silently leaving it out is what this exists to stop.
func TestINIRefusesAPopulatedNestedMember(t *testing.T) {
	rd, err := MapRunnerDefinition(`Nested`, `beat`, optionalNestedCfg{
		Ingester_UUID: uuid.New().String(),
		Tag_Name:      `test`,
		Filters:       []nestedSpec{{Field: `a`, Value: `b`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ini, err := rd.INI()
	if err == nil {
		t.Fatalf("a populated nested member was silently dropped:\n%s", ini)
	}
	if !errors.Is(err, ErrUnrepresentable) {
		t.Errorf("error does not wrap ErrUnrepresentable: %v", err)
	}
	if !strings.Contains(err.Error(), `Filters`) {
		t.Errorf("the error does not name the member: %v", err)
	}
}

// panickyCfg stands in for a plugin whose Verify blows up on a value an operator typed.
type panickyCfg struct {
	Ingester_UUID string
	Tag_Name      string
}

func (p *panickyCfg) Verify() error {
	if p.Tag_Name == `boom` {
		panic(`plugin Verify indexed past the end of something`)
	}
	return nil
}

// TestValidateSurvivesAPanickingPlugin is the regression guard on the crash.
//
// Validate is the one place this package calls into code it does not own, on data a
// webserver supplied.  Without a recover a plugin that panics takes the ingester down
// from the poll goroutine, and takes it down again on every restart for as long as the
// definition is still on the server.
func TestValidateSurvivesAPanickingPlugin(t *testing.T) {
	var n NopManager
	if err := n.RegisterKind(`panicky`, false, panickyCfg{}); err != nil {
		t.Fatal(err)
	}
	rd, err := MapRunnerDefinition(`panicky`, `beat`, panickyCfg{
		Ingester_UUID: uuid.New().String(), Tag_Name: `boom`,
	})
	if err != nil {
		t.Fatal(err)
	}

	// no recover here on purpose: if Validate lets a panic through, this test fails by
	// crashing the run, which is precisely what it is guarding against
	verr := n.Validate(rd)
	if verr == nil {
		t.Fatal(`a panicking Verify was reported as a valid configuration`)
	}
	if !errors.Is(verr, ErrInvalidRunner) {
		t.Errorf("error does not wrap ErrInvalidRunner: %v", verr)
	}
	if !strings.Contains(verr.Error(), `panic`) {
		t.Errorf("the reason does not say it panicked, so it would be indistinguishable from a normal rejection: %v", verr)
	}

	// and a definition the same plugin is happy with still passes, so the recover has not
	// simply turned the whole kind into a failure
	good, err := MapRunnerDefinition(`panicky`, `beat`, panickyCfg{
		Ingester_UUID: uuid.New().String(), Tag_Name: `fine`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = n.Validate(good); err != nil {
		t.Errorf("a valid configuration was rejected: %v", err)
	}
}

// TestSyncReportsAPanickingPluginRatherThanDying walks the same hazard down the path it
// actually reaches production on: the background poll, where a panic is unrecovered.
func TestSyncReportsAPanickingPluginRatherThanDying(t *testing.T) {
	dir := t.TempDir()
	dcm := newLoadManager(t, dir)
	if err := dcm.RegisterKind(`panicky`, false, panickyCfg{}); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	rd, err := MapRunnerDefinition(`panicky`, `beat`, panickyCfg{
		Ingester_UUID: id.String(), Tag_Name: `boom`,
	})
	if err != nil {
		t.Fatal(err)
	}
	rd.UUID = id
	if err = dcm.Sync([]RunnerDefinition{rd}); err != nil {
		t.Fatalf("Sync failed rather than rejecting the runner: %v", err)
	}
	var found bool
	for _, rs := range dcm.Statuses() {
		if rs.UUID == id {
			found = true
			if rs.OK() {
				t.Errorf("a runner whose Verify panicked reported clean: %+v", rs)
			}
		}
	}
	if !found {
		t.Error(`the runner was not reported at all`)
	}
	// nothing was written, because nothing was accepted
	if ents := confNames(t, dir); len(ents) != 0 {
		t.Errorf("a rejected runner was written to disk: %v", ents)
	}
}
