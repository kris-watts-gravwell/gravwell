/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
)

// field is one rendered input.  It is derived from the registered prototype, never from
// the browser, so a form post cannot introduce a variable the ingester never advertised
// or change one's declared type.
type field struct {
	Name        string
	Type        string
	Description string
	Required    bool

	// Kind drives which control is drawn: text, number, bool, textarea or json
	Control string
	// Value is the current value rendered into the control
	Value string
	// Checked is the rendered state for a bool
	Checked bool
	// HasValue says a secret is already set.  The value itself is never rendered, this
	// only drives the placeholder so an operator can tell "set" from "empty".
	HasValue bool
	// Values is one entry per input for a list control.  There is always at least one,
	// so a fresh form has somewhere to type.
	Values []string
}

// controlFor picks an input shape for a declared type.
func controlFor(vt dynamic.ValueType) string {
	switch string(vt) {
	case `secret`:
		// a secret is a string, but it is masked and its current value is never rendered
		return `secret`
	case `bool`:
		return `bool`
	case `int`, `uint`, `float`:
		return `number`
	case `[]string`:
		// one input per entry rather than a textarea, so an entry that contains a comma
		// or a newline is unambiguous and so adding and removing one is a click
		return `list`
	case `struct`, `[]struct`:
		// there is no sensible flat control for a nested structure, so the raw encoded
		// value is shown and edited as JSON rather than pretending otherwise
		return `json`
	}
	return `text`
}

// renderValue turns a stored value into what the control should display.
//
// A secret renders as nothing, always.  Putting a credential into a password input still
// puts it in the page source, where view-source, the browser's autofill store and any
// extension can all read it, so the value simply never leaves the server.
func renderValue(v any, vt dynamic.ValueType) string {
	if v == nil || controlFor(vt) == `secret` {
		return ``
	}
	switch controlFor(vt) {
	case `json`:
		if b, err := json.MarshalIndent(v, ``, `  `); err == nil {
			return string(b)
		}
	case `number`:
		switch n := v.(type) {
		case float64:
			// JSON decodes every number as a float, render whole values without the
			// trailing .0 that would otherwise show up on every int
			if n == float64(int64(n)) {
				return strconv.FormatInt(int64(n), 10)
			}
			return strconv.FormatFloat(n, 'f', -1, 64)
		}
	}
	return fmt.Sprintf("%v", v)
}

// toStringSlice normalizes the shapes a []string arrives in, which differ between a value
// that came straight from a plugin and one that made a round trip through JSON.
func toStringSlice(v any) (r []string, ok bool) {
	switch s := v.(type) {
	case []string:
		return s, true
	case []any:
		r = make([]string, 0, len(s))
		for _, e := range s {
			str, is := e.(string)
			if !is {
				return nil, false
			}
			r = append(r, str)
		}
		return r, true
	}
	return nil, false
}

// fieldsFor builds the form for a kind, filled in from cur when editing an existing
// runner and left empty when creating a new one.
//
// The prototype drives the whole thing.  A variable the prototype does not declare is not
// rendered and, on the way back in, is not accepted.
func fieldsFor(proto dynamic.RunnerDefinition, cur *dynamic.RunnerDefinition) (r []field) {
	existing := map[string]any{}
	if cur != nil {
		for _, v := range cur.Variables {
			existing[v.Name] = v.Value
		}
	}
	for _, v := range proto.Variables {
		f := field{
			Name:        v.Name,
			Type:        string(v.Type),
			Description: v.Description,
			Required:    v.Required,
			Control:     controlFor(v.Type),
		}
		val, ok := existing[v.Name]
		if !ok {
			// no configured value, fall back to whatever default the prototype carries
			val = v.Value
		}
		switch f.Control {
		case `bool`:
			f.Checked = truthy(val)
		case `secret`:
			f.HasValue = val != nil && val != ``
		case `list`:
			if set, ok := toStringSlice(val); ok {
				f.Values = set
			}
			if len(f.Values) == 0 {
				f.Values = []string{``} // always one row to type into
			}
		default:
			f.Value = renderValue(val, v.Type)
		}
		r = append(r, f)
	}
	return
}

// truthy reads a bool out of the shapes it turns up in.
func truthy(v any) bool {
	switch b := v.(type) {
	case bool:
		return b
	case string:
		ok, _ := strconv.ParseBool(b)
		return ok
	}
	return false
}

// parseForm turns a submitted form into variables, using the prototype as the schema.
//
// Every value is converted to the type the prototype declares and then run through the
// same Variable.Validate the ingester uses, so a config that cannot be represented is
// rejected here rather than at the far end.
func parseForm(proto dynamic.RunnerDefinition, form url.Values, cur *dynamic.RunnerDefinition) (vars []dynamic.Variable, err error) {
	stored := map[string]any{}
	if cur != nil {
		for _, v := range cur.Variables {
			stored[v.Name] = v.Value
		}
	}
	for _, pv := range proto.Variables {
		raw := strings.TrimSpace(form.Get(`var.` + pv.Name))
		v := dynamic.Variable{
			Name:        pv.Name,
			Type:        pv.Type,
			Description: pv.Description,
			Required:    pv.Required,
		}

		if controlFor(pv.Type) == `bool` {
			// an unchecked box sends nothing at all, which is how a false arrives
			v.Value = form.Get(`var.`+pv.Name) != ``
		} else if controlFor(pv.Type) == `secret` && raw == `` {
			// the form never renders a secret's value, so an empty box means "leave it
			// alone" rather than "clear it".  Clearing one is done by saving a new value.
			v.Value = stored[pv.Name]
			if v.Value == nil && pv.Required {
				return nil, fmt.Errorf("%s is required", pv.Name)
			}
		} else if controlFor(pv.Type) == `list` {
			// every row posts under the same name, so take them all and drop the blanks
			// an operator left behind
			var set []string
			for _, entry := range form[`var.`+pv.Name] {
				if entry = strings.TrimSpace(entry); entry != `` {
					set = append(set, entry)
				}
			}
			if len(set) == 0 {
				if pv.Required {
					return nil, fmt.Errorf("%s is required", pv.Name)
				}
				v.Value = nil
			} else {
				v.Value = set
			}
		} else if raw == `` {
			if pv.Required {
				return nil, fmt.Errorf("%s is required", pv.Name)
			}
			// left blank, describe the variable without a value rather than writing a
			// zero the operator did not ask for
			v.Value = nil
		} else if v.Value, err = convert(raw, pv.Type); err != nil {
			return nil, fmt.Errorf("%s: %w", pv.Name, err)
		}

		if v.Value != nil {
			if err = v.Validate(); err != nil {
				return nil, fmt.Errorf("%s: %w", pv.Name, err)
			}
		}
		vars = append(vars, v)
	}
	return
}

// convert parses a submitted string into the declared type.
func convert(raw string, vt dynamic.ValueType) (v any, err error) {
	switch string(vt) {
	case `bool`:
		return strconv.ParseBool(raw)
	case `int`:
		return strconv.ParseInt(raw, 10, 64)
	case `uint`:
		return strconv.ParseUint(raw, 10, 64)
	case `float`:
		return strconv.ParseFloat(raw, 64)
	case `uuid`:
		if _, err = uuid.Parse(raw); err != nil {
			return nil, fmt.Errorf("not a UUID %w", err)
		}
		return raw, nil
	case `[]string`:
		// the form posts one entry per input now, this is the fallback for a value that
		// still arrives as a single blob
		var set []string
		for _, line := range strings.Split(raw, "\n") {
			if line = strings.TrimSpace(line); line != `` {
				set = append(set, line)
			}
		}
		if len(set) == 0 {
			return nil, nil
		}
		return set, nil
	case `struct`, `[]struct`:
		var decoded any
		if err = json.Unmarshal([]byte(raw), &decoded); err != nil {
			return nil, fmt.Errorf("not valid JSON %w", err)
		}
		return decoded, nil
	case `string`, `secret`:
		return raw, nil
	}
	return nil, fmt.Errorf("unsupported type %s", vt)
}
