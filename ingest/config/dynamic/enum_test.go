/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"strings"
	"testing"
)

type enumCfg struct {
	Ingester_UUID string
	Mode          string   `dynamic:"enum=static|environment|ec2role"`
	Kinds         []string `dynamic:"enum=alpha|beta|gamma"`
	Key           string   `dynamic:"requiredif=Mode:|static"`
	Free          string
}

// TestEnumIsCarried covers the declaration reaching whoever draws the form.
func TestEnumIsCarried(t *testing.T) {
	rd, err := MapRunnerDefinition(`enum`, `probe`, enumCfg{})
	if err != nil {
		t.Fatal(err)
	}
	mode, ok := findVar(rd, `Mode`)
	if !ok {
		t.Fatal(`no Mode variable`)
	}
	if strings.Join(mode.Enum, `,`) != `static,environment,ec2role` {
		t.Errorf("Mode enum = %v", mode.Enum)
	}
	kinds, _ := findVar(rd, `Kinds`)
	if strings.Join(kinds.Enum, `,`) != `alpha,beta,gamma` {
		t.Errorf("Kinds enum = %v", kinds.Enum)
	}
	if free, _ := findVar(rd, `Free`); len(free.Enum) != 0 {
		t.Errorf("an untagged member picked up an enum: %v", free.Enum)
	}
}

// TestEnumIsEnforced is what stops a value outside the set being stored and only refused
// later by the plugin.
func TestEnumIsEnforced(t *testing.T) {
	rd, err := MapRunnerDefinition(`enum`, `probe`, enumCfg{})
	if err != nil {
		t.Fatal(err)
	}
	mode, _ := findVar(rd, `Mode`)
	if err = (Variable{Name: mode.Name, Type: mode.Type, Enum: mode.Enum, Value: `static`}).Validate(); err != nil {
		t.Errorf("a declared value was refused: %v", err)
	}
	err = (Variable{Name: mode.Name, Type: mode.Type, Enum: mode.Enum, Value: `nonsense`}).Validate()
	if err == nil {
		t.Fatal(`a value outside the enum was accepted`)
	}
	// the message has to say what is allowed, an operator reads this with no other context
	for _, must := range []string{`nonsense`, `static`, `environment`, `ec2role`} {
		if !strings.Contains(err.Error(), must) {
			t.Errorf("the error does not mention %q: %v", must, err)
		}
	}

	// a list is checked entry by entry
	kinds, _ := findVar(rd, `Kinds`)
	good := Variable{Name: kinds.Name, Type: kinds.Type, Enum: kinds.Enum, Value: []string{`alpha`, `gamma`}}
	if err = good.Validate(); err != nil {
		t.Errorf("declared list values were refused: %v", err)
	}
	bad := Variable{Name: kinds.Name, Type: kinds.Type, Enum: kinds.Enum, Value: []string{`alpha`, `delta`}}
	if err = bad.Validate(); err == nil {
		t.Error(`a list carrying an undeclared value was accepted`)
	}
	// and after a JSON round trip, where a list arrives as []any
	viaJSON := Variable{Name: kinds.Name, Type: kinds.Type, Enum: kinds.Enum, Value: []any{`alpha`, `delta`}}
	if err = viaJSON.Validate(); err == nil {
		t.Error(`an undeclared value survived a JSON round trip`)
	}
}

// TestRequiredWhen covers the conditional requirement: a member needed only alongside a
// particular choice elsewhere, which a flat form cannot express on its own.
func TestRequiredWhen(t *testing.T) {
	rd, err := MapRunnerDefinition(`enum`, `probe`, enumCfg{})
	if err != nil {
		t.Fatal(err)
	}
	key, ok := findVar(rd, `Key`)
	if !ok {
		t.Fatal(`no Key variable`)
	}
	if key.Required {
		t.Error(`a conditionally required member must not be marked always required`)
	}
	if key.RequiredWhen == nil || key.RequiredWhen.Field != `Mode` {
		t.Fatalf("no condition recorded: %+v", key.RequiredWhen)
	}

	set := func(mode any) RunnerDefinition {
		out := rd
		out.Variables = append([]Variable(nil), rd.Variables...)
		for i := range out.Variables {
			if out.Variables[i].Name == `Mode` {
				out.Variables[i].Value = mode
			}
		}
		return out
	}
	for _, tc := range []struct {
		mode any
		want bool
	}{
		{`static`, true},
		// unset means static, see sqs_common.GetCredentials, so the empty entry in the
		// condition has to match or leaving the field alone drops the requirement
		{nil, true},
		{``, true},
		{`environment`, false},
		{`ec2role`, false},
	} {
		if got := set(tc.mode).RequiredNow(key); got != tc.want {
			t.Errorf("Mode=%v: required = %v, want %v", tc.mode, got, tc.want)
		}
	}

	// an always-required member stays required whatever else is going on
	if !set(`ec2role`).RequiredNow(Variable{Name: `x`, Required: true}) {
		t.Error(`an unconditional requirement was dropped`)
	}
}

// TestBadEnumTagsAreRefused keeps a mistyped tag from silently doing nothing.
func TestBadEnumTagsAreRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    any
	}{
		{`enum on a number`, struct {
			N int `dynamic:"enum=1|2"`
		}{}},
		{`enum on a bool`, struct {
			B bool `dynamic:"enum=yes|no"`
		}{}},
		{`empty enum`, struct {
			S string `dynamic:"enum="`
		}{}},
		{`requiredif with no value`, struct {
			S string `dynamic:"requiredif=Other"`
		}{}},
		{`requiredif with no field`, struct {
			S string `dynamic:"requiredif=:static"`
		}{}},
		// a secret is masked and its value withheld, so there is nothing to offer a
		// choice of and nothing to check the choice against.  Accepted, the pair would be
		// inert in both directions.
		{`enum on a secret`, struct {
			S string `json:"-" dynamic:"secret,enum=a|b"`
		}{}},
		{`enum on a secret, tags reversed`, struct {
			S string `json:"-" dynamic:"enum=a|b,secret"`
		}{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := MapRunnerDefinition(`k`, `n`, tc.v); err == nil {
				t.Error(`a malformed tag was accepted, so it would silently do nothing`)
			}
		})
	}
}
