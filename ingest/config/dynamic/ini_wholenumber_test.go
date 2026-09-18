/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"encoding/json"
	"strings"
	"testing"

	"uuid"
)

// TestIniWholeNumberAfterJSON pins down that a whole number survives the journey a value
// actually takes: set somewhere, stored as JSON, read back, rendered to a config file.
//
// The round trip is not optional in this design -- a server keeps definitions as JSON and
// the wire is JSON -- so every int and uint reaches emitIniLine as a float64.  fmt's %v
// renders a million as 1e+06, which the loader on the ingester then refuses, and the only
// symptom is the ingester reporting an error against a configuration that looks fine.
func TestIniWholeNumberAfterJSON(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typ   ValueType
		value any
		want  string
	}{
		{`uint at a million`, typeUint, uint64(1000000), `Batch-Size=1000000`},
		{`int at a million`, typeInt, int64(1000000), `Batch-Size=1000000`},
		{`int beyond a billion`, typeInt, int64(123456789012), `Batch-Size=123456789012`},
		{`small uint`, typeUint, uint64(64), `Batch-Size=64`},
		{`zero`, typeInt, int64(0), `Batch-Size=0`},
		{`negative int`, typeInt, int64(-5), `Batch-Size=-5`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rd := RunnerDefinition{
				Kind: `okta`, Name: `one`, UUID: uuid.New(),
				Variables: []Variable{{Name: `Batch-Size`, Type: tc.typ, Value: tc.value}},
			}
			// the storage and wire round trip, which is what turns the value into a float64
			raw, err := json.Marshal(rd)
			if err != nil {
				t.Fatal(err)
			}
			var back RunnerDefinition
			if err = json.Unmarshal(raw, &back); err != nil {
				t.Fatal(err)
			}
			if _, ok := back.Variables[0].Value.(float64); !ok {
				t.Fatalf("expected the round trip to yield a float64, got %T",
					back.Variables[0].Value)
			}
			ini, err := back.INI()
			if err != nil {
				t.Fatalf("failed to render: %v", err)
			}
			if !strings.Contains(ini, tc.want) {
				t.Fatalf("expected %q in:\n%s", tc.want, ini)
			}
			if strings.Contains(ini, `e+`) {
				t.Fatalf("an exponent reached the config file:\n%s", ini)
			}
		})
	}
}
