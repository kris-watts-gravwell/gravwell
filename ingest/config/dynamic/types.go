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
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"
	"uuid"
)

const (
	// ingesterUUIDName is the config variable carrying the ingester UUID.  It is lifted out of
	// the variable list into Config.UUID so that INI does not emit it twice.
	ingesterUUIDName = `Ingester-UUID`

	// maxStructDepth guards against a config type that manages to reference itself.
	maxStructDepth = 8

	// dynamicTag is the struct tag this package reads for per member options:
	//
	//	Token string `dynamic:"secret"`
	//
	// It is separate from the gcfg and json tags because it describes how a member should
	// be presented and handled by the dynamic configuration system, not how it is parsed
	// or serialized.
	dynamicTag = `dynamic`

	// optSecret marks a member as a secret: a string whose value must never be shipped
	// and which a GUI should mask rather than display.
	optSecret = `secret`

	// optRequired marks a member that has to be populated for the configuration to be
	// usable.  It describes the configuration to whoever is filling it in, it is not a
	// substitute for the plugin's own Verify: a config can arrive from somewhere other
	// than a form.
	optRequired = `required`

	// iniRawUnsafe is the set of runes the gcfg raw string scanner treats specially:
	// a backtick, a backslash and a double quote.  A value holding none of them is
	// copied through a backtick string verbatim.
	iniRawUnsafe = "`" + `\"`
)

var (
	ErrInvalidValueType = errors.New("invalid value type")
	ErrNotAStruct       = errors.New("config must be a struct")
	ErrUnsupportedType  = errors.New("unsupported config field type")
	ErrUnrepresentable  = errors.New("value cannot be represented in a config file")
	ErrAmbiguousMember  = errors.New("ambiguous promoted config member")

	uuidType = reflect.TypeFor[uuid.UUID]()
)

type ValueType string

const (
	typeBool        ValueType = `bool`
	typeInt         ValueType = `int`
	typeUint        ValueType = `uint`
	typeFloat       ValueType = `float`
	typeString      ValueType = `string`
	typeSecret      ValueType = `secret` // identical to a string, but we tell everyone we want to hide it
	typeUUID        ValueType = `uuid`
	typeSliceString ValueType = `[]string`
	typeStruct      ValueType = `struct`
	typeSliceStruct ValueType = `[]struct`
)

// RunnerDefinition represents a description of an ingester config or a populated and configured ingester.
// This struct is used to translate a native RunnerDefinition type from a plugin into something that can be shipped over JSON
// to a GUI/Webserver and drawn in a human friendly way.  It can then be sent to the ingester to be validated and
// translated back to an INI config blob.
type RunnerDefinition struct {
	Kind      string    // what configuration type this represents
	Name      string    // config name (key in config map for a given Kind)
	UUID      uuid.UUID `json:",omitzero"`
	Singleton bool      // whether more than one of this Kind can run at a time
	Variables []Variable
	Assigned  *Assignment `json:",omitempty"` // optional asisgnment for this config, will be empty for empty config prototypes
}

// Variable is a single value in a dynamic configuration, it may be some primative
// type like a string, int, float, etc.. or it may be a vector of primative types.
type Variable struct {
	Name        string
	Value       any `json:",omitempty"`
	Type        ValueType
	Description string `json:",omitempty"`
	Required    bool
}

// Assignment narrows which ingesters a configuration is meant for.  An empty Assignment,
// or a nil one, means the configuration goes to anything able to run its kind.
//
// The two lists are independent filters and both have to pass.  A configuration listing
// UUIDs goes only to those ingesters, one listing Classes goes only to ingesters in one
// of those classes, and one listing both goes only to an ingester that satisfies each.
// An empty list is not a filter, so listing classes alone does not restrict by UUID.
type Assignment struct {
	UUIDs   []uuid.UUID `json:",omitempty"` // specific ingester instances
	Classes []string    `json:",omitempty"` // classes of ingesters (hosted, http, etc...)
	Group   string      `json:",omitempty"` // a free form group (user configured)
}

// Empty reports whether this assignment narrows anything at all.
func (a *Assignment) Empty() bool {
	return a == nil || (len(a.UUIDs) == 0 && len(a.Classes) == 0 && a.Group == ``)
}

// AllowsUUID reports whether an ingester's UUID passes the UUID filter.  An assignment
// that lists no UUIDs does not filter on them.
func (a *Assignment) AllowsUUID(id uuid.UUID) bool {
	if a == nil || len(a.UUIDs) == 0 {
		return true
	}
	for _, cur := range a.UUIDs {
		if cur == id {
			return true
		}
	}
	return false
}

// AllowsClass reports whether an ingester's class passes the class filter.  An assignment
// that lists no classes does not filter on them.
func (a *Assignment) AllowsClass(class string) bool {
	if a == nil || len(a.Classes) == 0 {
		return true
	}
	for _, cur := range a.Classes {
		if cur == class {
			return true
		}
	}
	return false
}

// Validate checks a variable to make sure it is appropriately populated and that if there is a Value its type
// matches the ValueType
func (v Variable) Validate() error {
	if v.Name == `` {
		return errors.New("missing Name")
	} else if v.Type == `` {
		return errors.New("Missing Type")
	} else if err := v.Type.Valid(); err != nil {
		return err
	}
	if v.Value == nil {
		return nil // nothing to check, an empty prototype or an unset variable is fine
	}
	switch val := v.Value.(type) {
	case bool:
		return v.requireType(typeBool)
	case int, int8, int16, int32, int64:
		return v.requireType(typeInt)
	case uint, uint8, uint16, uint32, uint64:
		return v.requireType(typeUint)
	case float32:
		return v.requireType(typeFloat)
	case float64:
		// encoding/json hands back every number as a float64, so an integral
		// float64 is also an acceptable int or uint
		switch v.Type {
		case typeFloat:
		case typeInt:
			if val != math.Trunc(val) {
				return v.typeMismatch()
			}
		case typeUint:
			if val != math.Trunc(val) || val < 0 {
				return v.typeMismatch()
			}
		default:
			return v.typeMismatch()
		}
	case string:
		switch v.Type {
		case typeString:
		case typeSecret:
		case typeUUID:
			if _, err := uuid.Parse(val); err != nil {
				return fmt.Errorf("Value %q is not a valid uuid: %w", val, err)
			}
		default:
			return v.typeMismatch()
		}
	case uuid.UUID:
		return v.requireType(typeUUID)
	case []string:
		return v.requireType(typeSliceString)
	case []any:
		// encoding/json hands back every array as a []any, so walk it to see what we really have
		switch v.Type {
		case typeSliceString:
			for i, x := range val {
				if _, ok := x.(string); !ok {
					return fmt.Errorf("Value element %d is a %T, not a string", i, x)
				}
			}
		case typeSliceStruct:
			for i, x := range val {
				if _, ok := x.(map[string]any); !ok {
					return fmt.Errorf("Value element %d is a %T, not a struct", i, x)
				}
			}
		default:
			return v.typeMismatch()
		}
	case map[string]any:
		return v.requireType(typeStruct)
	case []map[string]any:
		return v.requireType(typeSliceStruct)
	case []Variable:
		if v.Type != typeStruct {
			return v.typeMismatch()
		}
		for _, sub := range val {
			if err := sub.Validate(); err != nil {
				return fmt.Errorf("member %s is invalid: %w", sub.Name, err)
			}
		}
	case [][]Variable:
		if v.Type != typeSliceStruct {
			return v.typeMismatch()
		}
		for i, members := range val {
			for _, sub := range members {
				if err := sub.Validate(); err != nil {
					return fmt.Errorf("element %d member %s is invalid: %w", i, sub.Name, err)
				}
			}
		}
	default:
		return fmt.Errorf("Value type %T is not a supported dynamic config type", v.Value)
	}
	return nil
}

// requireType checks that the Variable is declared as the given ValueType.
func (v Variable) requireType(vt ValueType) error {
	if v.Type != vt {
		return v.typeMismatch()
	}
	return nil
}

// iniValueString renders s for the right hand side of an INI assignment.
//
// A raw backtick string is used when it is provably safe, purely because it is far easier
// to read in a config file.  The gcfg raw string scanner treats exactly three runes
// specially: a backtick terminates the string, a backslash sets its escape state, and a
// double quote gets escaped.  Every other rune is copied through verbatim, so a value
// holding none of those three cannot be altered by it no matter how that escaping logic
// changes.  Anything else takes the double quoted path, which is total over the values we
// support, so this is an optimization and never a correctness requirement.
func iniValueString(s string) (string, error) {
	if !strings.ContainsAny(s, iniRawUnsafe) {
		return "`" + s + "`", nil
	}
	return iniQuote(s)
}

// iniQuote renders s as a gcfg double quoted string.  This is the general path and can
// carry any value a config supports, including backticks and embedded newlines.
//
// The one exception is control characters.  The gcfg scanner understands exactly four
// escapes, \\ \" \n and \t, and has no numeric escape, so a control character other than
// a newline or a tab simply cannot be written into a config file.  Configs are not
// expected to carry them, so rather than mangle the value we reject it with
// ErrUnrepresentable and let the caller report a real error.
func iniQuote(s string) (string, error) {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			sb.WriteString(`\\`)
		case '"':
			sb.WriteString(`\"`)
		case '\n':
			sb.WriteString(`\n`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				return ``, fmt.Errorf("%w: control character %q", ErrUnrepresentable, r)
			}
			sb.WriteRune(r)
		}
	}
	sb.WriteByte('"')
	return sb.String(), nil
}

// emitInitLine just encodes a variable into our INI line format
func (v Variable) emitIniLine(w io.Writer, prefix string) (err error) {
	// if the value is empty, do nothing
	if v.Value == nil {
		return
	}
	switch v.Type {
	case typeBool, typeInt, typeUint:
		fmt.Fprintf(w, "%s%s=%v\n", prefix, v.Name, v.Value)
	case typeFloat:
		// %v uses the shortest representation that parses back exactly, %f would
		// silently truncate the value to six decimal places
		fmt.Fprintf(w, "%s%s=%v\n", prefix, v.Name, v.Value)
	case typeSecret:
		fallthrough // identical to a string
	case typeString:
		var q string
		if q, err = v.quote(fmt.Sprintf("%s", v.Value)); err != nil {
			return
		}
		fmt.Fprintf(w, "%s%s=%s\n", prefix, v.Name, q)
	case typeUUID:
		// a uuid is only ever hex and dashes, it can never need escaping
		switch uv := v.Value.(type) {
		case string:
			fmt.Fprintf(w, "%s%s=%q\n", prefix, v.Name, uv)
		case uuid.UUID:
			fmt.Fprintf(w, "%s%s=%q\n", prefix, v.Name, uv.String())
		default:
			err = v.typeMismatch()
		}
	case typeSliceString:
		var set []string
		if set, err = v.stringSet(); err != nil {
			return
		}
		for _, x := range set {
			var q string
			if q, err = v.quote(x); err != nil {
				return
			}
			fmt.Fprintf(w, "%s%s=%s\n", prefix, v.Name, q)
		}
	case typeStruct:
		// TODO
	case typeSliceStruct:
		// TODO
	}
	return
}

// quote renders one of this variable's strings for an INI line, naming the variable on failure.
func (v Variable) quote(s string) (q string, err error) {
	if q, err = iniValueString(s); err != nil {
		err = fmt.Errorf("%s: %w", v.Name, err)
	}
	return
}

// stringSet pulls a typeSliceString value out as a []string.  A JSON round trip hands the
// slice back as a []any, so accept that shape too rather than silently emitting nothing.
func (v Variable) stringSet() (set []string, err error) {
	switch sv := v.Value.(type) {
	case []string:
		set = sv
	case []any:
		set = make([]string, 0, len(sv))
		for i, x := range sv {
			str, ok := x.(string)
			if !ok {
				err = fmt.Errorf("%s element %d is a %T, not a string", v.Name, i, x)
				return
			}
			set = append(set, str)
		}
	default:
		err = fmt.Errorf("%s: %w", v.Name, v.typeMismatch())
	}
	return
}

// typeMismatch reports that the concrete type of Value disagrees with the declared ValueType.
func (v Variable) typeMismatch() error {
	return fmt.Errorf("Value type %T does not match ValueType %s", v.Value, v.Type)
}

// ValidType checks that the given ValueType is one that we know how to handle.
// An unrecognized type results in an error wrapping ErrInvalidValueType.
func (vt ValueType) Valid() (err error) {
	switch vt {
	case typeBool:
	case typeInt:
	case typeUint:
	case typeFloat:
	case typeString:
	case typeSecret:
	case typeUUID:
	case typeSliceString:
	case typeStruct:
	case typeSliceStruct:
	default:
		err = fmt.Errorf("%w %q", ErrInvalidValueType, string(vt))
	}
	return
}

// Complex indicates if a ValueType is not a primitive type.
// non-primative types include slices and structs
func (vt ValueType) Complex() bool {
	switch vt {
	case typeBool:
	case typeInt:
	case typeUint:
	case typeFloat:
	case typeString:
	case typeSecret:
	case typeUUID:
	case typeSliceString:
	default:
		return true
	}
	return false
}

// MapRunnerDefinition takes a native plugin config struct and maps it to the dynamic RunnerDefinition structure.
// Embedded structs are flattened, because gcfg promotes them into the parent INI section.
// Unexported members are skipped, they are derived at verify time and are not config.
// A member tagged dynamic:"secret" is described as a secret rather than a string, and a
// member tagged either dynamic:"secret" or json:"-" has its value withheld: the variable
// is described so a GUI can ask for it, but the current value is never populated.
// A member tagged dynamic:"required" is marked Required so a form can insist on it.  The
// options combine, dynamic:"secret,required" is a credential that has to be supplied.  Zero valued members are described without a value, so handing this a zero
// struct yields an empty prototype and handing it a populated struct yields a populated config.
func MapRunnerDefinition(kind, name string, v any) (c RunnerDefinition, err error) {
	if kind == `` {
		err = errors.New("empty kind")
		return
	} else if name == `` {
		err = errors.New("empty name")
		return
	} else if v == nil {
		err = fmt.Errorf("%w, got nil", ErrNotAStruct)
		return
	}
	rv := derefValue(reflect.ValueOf(v))
	if rv.Kind() != reflect.Struct {
		err = fmt.Errorf("%w, got %T", ErrNotAStruct, v)
		return
	}
	var vars []Variable
	if vars, err = mapStruct(rv, 0); err != nil {
		return
	}
	c = RunnerDefinition{Kind: kind, Name: name, Variables: make([]Variable, 0, len(vars))}
	for _, vr := range vars {
		if err = vr.Validate(); err != nil {
			err = fmt.Errorf("%s produced an invalid variable: %w", vr.Name, err)
			return
		}
		if vr.Name == ingesterUUIDName {
			// INI writes this from RunnerDefinition.UUID, keep it out of the variable set
			if s, ok := vr.Value.(string); ok {
				if c.UUID, err = uuid.Parse(s); err != nil {
					err = fmt.Errorf("Invalid Ingester-UUID %q %w", s, err)
					return
				}
			}
			continue
		}
		c.Variables = append(c.Variables, vr)
	}
	return
}

// mappedVar is a Variable plus how far it was promoted to reach the struct being
// flattened.  A member declared directly on that struct is depth 0, one pulled out of an
// embedded struct is depth 1, and so on.
type mappedVar struct {
	Variable
	depth int
}

// mapStruct walks the exported members of a struct and produces a Variable for each,
// flattening any embedded structs into the same list because gcfg promotes them into the
// parent INI section rather than giving them a subsection.
func mapStruct(rv reflect.Value, depth int) (vars []Variable, err error) {
	var mapped []mappedVar
	if mapped, err = mapStructMembers(rv, depth); err != nil {
		return
	}
	return resolveShadowed(mapped)
}

// mapStructMembers does the actual walk, tracking promotion depth so that the caller can
// apply the shadowing rules.
func mapStructMembers(rv reflect.Value, depth int) (vars []mappedVar, err error) {
	if depth > maxStructDepth {
		err = fmt.Errorf("config is nested deeper than %d structs", maxStructDepth)
		return
	}
	rt := rv.Type()
	for i := range rt.NumField() {
		f := rt.Field(i)
		// An embedded struct promotes its exported members even when the embedded type
		// itself is unexported, and gcfg populates them happily, so recurse into it
		// before the export check that skips an ordinary unexported member.  This is the
		// same rule encoding/json applies when it walks a struct.
		if f.Anonymous && derefType(f.Type).Kind() == reflect.Struct {
			var sub []mappedVar
			if sub, err = mapStructMembers(derefValue(rv.Field(i)), depth+1); err != nil {
				return
			}
			for _, sv := range sub {
				sv.depth++ // one promotion further out than where it was declared
				vars = append(vars, sv)
			}
			continue
		}
		if !f.IsExported() {
			continue
		}
		var nv Variable
		if nv, err = mapField(f, rv.Field(i), depth); err != nil {
			return
		}
		vars = append(vars, mappedVar{Variable: nv})
	}
	return
}

// resolveShadowed applies Go's own field promotion rules to a flattened member list: a
// member declared closer to the outside shadows a promoted one of the same name.  That
// matters because gcfg resolves an INI key the same way, so emitting both would write a
// key that only ever lands on the outer member and silently leave the inner one unset.
// Two members promoted from the same depth are genuinely ambiguous, neither Go nor gcfg
// can address them, so that is reported rather than guessed at.
func resolveShadowed(mapped []mappedVar) (vars []Variable, err error) {
	shallowest := make(map[string]int, len(mapped))
	ambiguous := make(map[string]bool, len(mapped))
	for _, m := range mapped {
		switch d, ok := shallowest[m.Name]; {
		case !ok || m.depth < d:
			shallowest[m.Name] = m.depth
			ambiguous[m.Name] = false
		case m.depth == d:
			ambiguous[m.Name] = true
		}
	}
	vars = make([]Variable, 0, len(mapped))
	seen := make(map[string]bool, len(mapped))
	for _, m := range mapped {
		if m.depth != shallowest[m.Name] || seen[m.Name] {
			continue // shadowed by a member closer to the outside, or already emitted
		}
		if ambiguous[m.Name] {
			err = fmt.Errorf("%w %q", ErrAmbiguousMember, m.Name)
			return
		}
		seen[m.Name] = true
		vars = append(vars, m.Variable)
	}
	return
}

// mapField produces a single Variable from a struct member.
func mapField(f reflect.StructField, fv reflect.Value, depth int) (v Variable, err error) {
	v.Name = iniName(f)
	ft := derefType(f.Type)
	if v.Type, err = valueTypeOf(ft); err != nil {
		err = fmt.Errorf("%s: %w", v.Name, err)
		return
	}
	// a member tagged dynamic:"required" has to be filled in, which lets a form say so
	// up front rather than letting someone save a configuration the ingester will refuse
	v.Required = hasDynamicOption(f, optRequired)

	// a member tagged dynamic:"secret" is a string that a GUI should mask.  It is typed
	// as a secret rather than a string so that every consumer knows, rather than each one
	// having to guess from the member's name.
	secret := hasDynamicOption(f, optSecret)
	if secret {
		if v.Type != typeString {
			err = fmt.Errorf("%s: %w, a secret must be a string, got %s", v.Name, ErrUnsupportedType, ft)
			return
		}
		v.Type = typeSecret
	}
	// a secret's value never leaves the ingester, and neither does a json:"-" member's.
	// Both are described so a GUI can ask for them, but the current value is withheld:
	// shipping a live credential to a browser to render is how they escape.
	if secret || f.Tag.Get(`json`) == `-` {
		return
	}
	if fv = derefValue(fv); !fv.IsValid() || fv.IsZero() {
		return // unset, leave Value nil
	}
	if v.Value, err = fieldValue(v.Type, fv, depth); err != nil {
		err = fmt.Errorf("%s: %w", v.Name, err)
	}
	return
}

// hasDynamicOption reports whether a member carries an option in its dynamic tag.
//
// The tag value is a comma separated list so that further options can be added without
// invalidating the ones already written into plugin configs.  Note the spelling: Go
// struct tags are `dynamic:"secret"`, a space after the colon stops the conventional tag
// parser from finding the key at all.
func hasDynamicOption(f reflect.StructField, opt string) bool {
	for _, cur := range strings.Split(f.Tag.Get(dynamicTag), `,`) {
		if strings.TrimSpace(cur) == opt {
			return true
		}
	}
	return false
}

// iniName returns the name gcfg would match this member against, honoring a gcfg ident
// override and otherwise swapping the underscores in the Go name for dashes.
func iniName(f reflect.StructField) string {
	if ident, _, _ := strings.Cut(f.Tag.Get(`gcfg`), `,`); ident != `` {
		return ident
	}
	return strings.ReplaceAll(f.Name, `_`, `-`)
}

// valueTypeOf maps a Go type onto the ValueType used to describe it.
func valueTypeOf(t reflect.Type) (vt ValueType, err error) {
	if t == uuidType {
		return typeUUID, nil
	}
	switch t.Kind() {
	case reflect.Bool:
		vt = typeBool
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		vt = typeInt
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		vt = typeUint
	case reflect.Float32, reflect.Float64:
		vt = typeFloat
	case reflect.String:
		vt = typeString // named string types (mimecast Api, msgraph ContentType) land here too
	case reflect.Struct:
		vt = typeStruct
	case reflect.Slice:
		et := derefType(t.Elem())
		switch {
		case et == uuidType:
			err = fmt.Errorf("%w %s", ErrUnsupportedType, t)
		case et.Kind() == reflect.String:
			vt = typeSliceString
		case et.Kind() == reflect.Struct:
			vt = typeSliceStruct
		default:
			err = fmt.Errorf("%w %s", ErrUnsupportedType, t)
		}
	default:
		err = fmt.Errorf("%w %s", ErrUnsupportedType, t)
	}
	return
}

// fieldValue extracts a member's value in the representation Variable.Validate expects.
func fieldValue(vt ValueType, fv reflect.Value, depth int) (val any, err error) {
	switch vt {
	case typeBool:
		val = fv.Bool()
	case typeInt:
		val = fv.Int()
	case typeUint:
		val = fv.Uint()
	case typeFloat:
		val = fv.Float()
	case typeString, typeSecret:
		val = fv.String()
	case typeUUID:
		val = fv.Interface().(uuid.UUID).String()
	case typeSliceString:
		set := make([]string, fv.Len())
		for i := range set {
			set[i] = derefValue(fv.Index(i)).String()
		}
		val = set
	case typeStruct:
		var sub []Variable
		if sub, err = mapStruct(fv, depth+1); err != nil {
			return
		}
		val = sub
	case typeSliceStruct:
		set := make([][]Variable, fv.Len())
		for i := range set {
			if set[i], err = mapStruct(derefValue(fv.Index(i)), depth+1); err != nil {
				return
			}
		}
		val = set
	default:
		err = fmt.Errorf("%w %s", ErrInvalidValueType, vt)
	}
	return
}

// derefType follows pointers to the type actually being pointed at.
func derefType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// derefValue follows pointers to the value actually being pointed at.  A nil pointer yields
// a zero value of the pointed at type so that we can still describe the variable.
func derefValue(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return reflect.New(v.Type().Elem()).Elem()
		}
		v = v.Elem()
	}
	return v
}

// INI writes out a
func (c RunnerDefinition) INI() (r string, err error) {
	// check the required stuff
	if c.Kind == `` {
		err = errors.New("empty kind")
		return
	} else if c.Name == `` {
		err = errors.New("empty name")
		return
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[%s %q]\n", c.Kind, c.Name)
	if c.UUID != uuid.Nil() {
		fmt.Fprintf(&sb, "\tIngester-UUID=%s\n", c.UUID)
	}
	var todo []Variable
	for _, v := range c.Variables {
		// throw the complex variables on the end to do last
		if v.Type.Complex() {
			todo = append(todo, v)
			continue
		} else if err = v.emitIniLine(&sb, "\t"); err != nil {
			return
		}
	}
	r = sb.String()
	return
}
