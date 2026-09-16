/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"reflect"
	"sort"
	"testing"
	"uuid"

	"github.com/gravwell/gravwell/v4/hosted/plugins"
	"github.com/gravwell/gravwell/v4/ingest/config"
	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
)

// pluginMembers reports the members of plugins.Configs by reflection.  The tests derive
// what they expect from the config set itself rather than from a list, so adding a plugin
// does not mean coming back here to update anything.
func pluginMembers(t *testing.T) []string {
	t.Helper()
	rt := reflect.TypeOf(plugins.Configs{})
	names := make([]string, 0, rt.NumField())
	for i := 0; i < rt.NumField(); i++ {
		if f := rt.Field(i); f.IsExported() {
			names = append(names, f.Name)
		}
	}
	if len(names) == 0 {
		t.Fatal(`plugins.Configs has no members, the tests below would prove nothing`)
	}
	sort.Strings(names)
	return names
}

// nopRunner builds the no op manager the runner uses when dynamic config is disabled.
func nopRunner() *dynamic.NopManager {
	return &dynamic.NopManager{}
}

// TestRegisterDynamicPluginTypes checks that every plugin carried in plugins.Configs is
// advertised to the manager, with no list to maintain on either side.
func TestRegisterDynamicPluginTypes(t *testing.T) {
	nm := nopRunner()
	if err := registerDynamicPluginTypes(nm); err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(nm.Available))
	for _, rd := range nm.Available {
		got = append(got, rd.Kind)
	}
	sort.Strings(got)
	if want := pluginMembers(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("registered %v, want every plugin in Configs %v", got, want)
	}

	// a kind is a description, it carries no identity and every plugin can run more than
	// one instance at a time, so none of them are singletons
	for _, rd := range nm.Available {
		if rd.Name != `` || rd.UUID != uuid.Nil() {
			t.Errorf("%s: a kind should carry no identity, got name %q uuid %v", rd.Kind, rd.Name, rd.UUID)
		}
		if rd.Singleton {
			t.Errorf("%s: plugins are held in maps, none of them are singletons", rd.Kind)
		}
		if len(rd.Variables) == 0 {
			t.Errorf("%s: registered with no variables, its config did not enumerate", rd.Kind)
		}
	}

	// a nil manager is a wiring mistake, not a panic
	if err := registerDynamicPluginTypes(nil); err == nil {
		t.Error(`a nil manager should be rejected`)
	}
}

// TestRegisterDynamicPluginTypesIdempotent checks that registering twice is reported
// rather than silently producing duplicates.
func TestRegisterDynamicPluginTypesIdempotent(t *testing.T) {
	nm := nopRunner()
	if err := registerDynamicPluginTypes(nm); err != nil {
		t.Fatal(err)
	}
	n := len(nm.Available)
	if err := registerDynamicPluginTypes(nm); err == nil {
		t.Error(`registering the same kinds twice should be rejected`)
	}
	if len(nm.Available) != n {
		t.Errorf("Available = %d, want %d, a rejected registration should add nothing", len(nm.Available), n)
	}
}

// TestNopRunnerLoadsEveryPluginConfig is the disabled dynamic config path: the runner
// still advertises every plugin and can take a configured runner for each one.
func TestNopRunnerLoadsEveryPluginConfig(t *testing.T) {
	nm := nopRunner()
	if err := registerDynamicPluginTypes(nm); err != nil {
		t.Fatal(err)
	}
	kinds, err := plugins.Kinds()
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range kinds {
		if err = nm.RegisterRunner(`prod`, pk.Kind, uuid.Nil(), pk.Config); err != nil {
			t.Errorf("%s: %v", pk.Kind, err)
		}
	}
	if len(nm.Configured) != len(kinds) {
		t.Fatalf("configured %d runners, want %d", len(nm.Configured), len(kinds))
	}
	for _, cr := range nm.Configured {
		if cr.UUID == uuid.Nil() {
			t.Errorf("%s: a UUID should have been generated", cr.Kind)
		}
	}
}

// TestDynamicRunnerLoadsEveryPluginConfig is the point of the whole exercise: a config
// written for any plugin has to be readable back into the very struct the runner uses.
func TestDynamicRunnerLoadsEveryPluginConfig(t *testing.T) {
	dir := t.TempDir()
	dm, err := dynamic.NewDynamicConfigManager(nil, dynamic.Config{
		Webserver:  []string{`10.0.0.1:8080`},
		Auth_Token: `token`,
		Storage:    dir,
	}, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()

	if err = registerDynamicPluginTypes(dm); err != nil {
		t.Fatal(err)
	}
	kinds, err := plugins.Kinds()
	if err != nil {
		t.Fatal(err)
	}
	// two instances of every plugin, the names have to survive as the map keys
	names := []string{`prod`, `dev`}
	for _, pk := range kinds {
		for _, name := range names {
			if err = dm.RegisterRunner(name, pk.Kind, uuid.New(), pk.Config); err != nil {
				t.Fatalf("%s %s: %v", pk.Kind, name, err)
			}
		}
	}

	// read every generated config back into the struct the runner actually parses into
	var cfgs plugins.Configs
	if err = config.LoadConfigOverlays(&cfgs, dir); err != nil {
		t.Fatalf("the generated plugin configs do not load back: %v", err)
	}
	// every member of Configs has to have been populated, by reflection so that a new
	// plugin is covered here without touching this test
	rv := reflect.ValueOf(cfgs)
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		m := rv.Field(i)
		if m.Len() != len(names) {
			t.Errorf("Configs.%s holds %d configs, want %d", f.Name, m.Len(), len(names))
			continue
		}
		for _, name := range names {
			e := m.MapIndex(reflect.ValueOf(name))
			if !e.IsValid() {
				t.Errorf("Configs.%s is missing %q", f.Name, name)
			} else if e.Kind() == reflect.Pointer && e.IsNil() {
				t.Errorf("Configs.%s[%q] loaded as nil", f.Name, name)
			}
		}
	}
}

// TestPluginKindsDerivedFromConfigs guards the reflection itself, a plugin added to
// Configs in a shape the dynamic system cannot describe has to be reported loudly rather
// than quietly left out.
func TestPluginKindsDerivedFromConfigs(t *testing.T) {
	kinds, err := plugins.Kinds()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(kinds))
	seen := map[string]bool{}
	for _, pk := range kinds {
		if pk.Kind == `` {
			t.Error(`a kind with no name`)
		}
		if seen[pk.Kind] {
			t.Errorf("%s appears twice", pk.Kind)
		}
		seen[pk.Kind] = true
		// the config has to be a zero valued struct, it describes a type and must never
		// carry data from whatever the caller happened to have lying around
		rv := reflect.ValueOf(pk.Config)
		if rv.Kind() != reflect.Struct {
			t.Errorf("%s: config is a %s, want a struct", pk.Kind, rv.Kind())
		} else if !rv.IsZero() {
			t.Errorf("%s: config is not zero valued", pk.Kind)
		}
		got = append(got, pk.Kind)
	}
	sort.Strings(got)
	if want := pluginMembers(t); !reflect.DeepEqual(got, want) {
		t.Errorf("Kinds() = %v, want every member of Configs %v", got, want)
	}
}

// TestConfigsIngesterCountCoversEveryPlugin checks the count the runner actually gates
// startup on.  main refuses to start when IngesterCount is zero, so a count that misses a
// plugin means a runner configured with only that plugin exits with "no hosted ingesters
// configured".  Counting is derived from Configs now, this pins the behaviour so a return
// to a hand written sum is caught.
func TestConfigsIngesterCountCoversEveryPlugin(t *testing.T) {
	kinds, err := plugins.Kinds()
	if err != nil {
		t.Fatal(err)
	}

	// every plugin on its own has to be enough to start the runner
	for _, pk := range kinds {
		dir := t.TempDir()
		var dm dynamic.Manager
		if dm, err = dynamic.NewDynamicConfigManager(nil, dynamic.Config{
			Webserver:  []string{`10.0.0.1:8080`},
			Auth_Token: `token`,
			Storage:    dir,
		}, uuid.New(), nil); err != nil {
			t.Fatal(err)
		}
		if err = registerDynamicPluginTypes(dm); err != nil {
			dm.Close()
			t.Fatal(err)
		}
		if err = dm.RegisterRunner(`prod`, pk.Kind, uuid.New(), pk.Config); err != nil {
			dm.Close()
			t.Fatalf("%s: %v", pk.Kind, err)
		}
		dm.Close()

		var cfgs plugins.Configs
		if err = config.LoadConfigOverlays(&cfgs, dir); err != nil {
			t.Fatalf("%s: %v", pk.Kind, err)
		}
		if n := cfgs.IngesterCount(); n != 1 {
			t.Errorf("%s alone counted %d ingesters, want 1, the runner would refuse to start", pk.Kind, n)
		}
	}

	// and the counts have to add up across all of them at once
	dir := t.TempDir()
	dm, err := dynamic.NewDynamicConfigManager(nil, dynamic.Config{
		Webserver:  []string{`10.0.0.1:8080`},
		Auth_Token: `token`,
		Storage:    dir,
	}, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	if err = registerDynamicPluginTypes(dm); err != nil {
		t.Fatal(err)
	}
	names := []string{`prod`, `dev`}
	for _, pk := range kinds {
		for _, name := range names {
			if err = dm.RegisterRunner(name, pk.Kind, uuid.New(), pk.Config); err != nil {
				t.Fatalf("%s %s: %v", pk.Kind, name, err)
			}
		}
	}
	var cfgs plugins.Configs
	if err = config.LoadConfigOverlays(&cfgs, dir); err != nil {
		t.Fatal(err)
	}
	if got, want := cfgs.IngesterCount(), len(kinds)*len(names); got != want {
		t.Errorf("IngesterCount = %d, want %d, one config per plugin per name", got, want)
	}

	// an empty config set counts nothing, that is what makes the startup gate meaningful
	if n := (plugins.Configs{}).IngesterCount(); n != 0 {
		t.Errorf("an empty Configs counted %d, want 0", n)
	}
}

// TestDynamicLoadIntoRunnerConfig pins the pointer shape the reload path has to hand the
// dynamic manager.  newCfg in main is already a *cfgType, so passing &newCfg gives the
// overlay loader a **cfgType, which it rejects.  That aborts the whole reload, so with
// dynamic config enabled a SIGHUP would quietly stop reloading anything.
func TestDynamicLoadIntoRunnerConfig(t *testing.T) {
	dir := t.TempDir()
	dm, err := dynamic.NewDynamicConfigManager(nil, dynamic.Config{
		Webserver:  []string{`10.0.0.1:8080`},
		Auth_Token: `token`,
		Storage:    dir,
	}, uuid.New(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dm.Close()
	if err = registerDynamicPluginTypes(dm); err != nil {
		t.Fatal(err)
	}
	kinds, err := plugins.Kinds()
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range kinds {
		if err = dm.RegisterRunner(`prod`, pk.Kind, uuid.New(), pk.Config); err != nil {
			t.Fatalf("%s: %v", pk.Kind, err)
		}
	}

	// the shape the reload path uses, a *cfgType, every dynamic config has to land in it
	newCfg := &cfgType{}
	if err = dm.Load(newCfg); err != nil {
		t.Fatalf("the reload path cannot load dynamic configs: %v", err)
	}
	if got, want := newCfg.IngesterCount(), len(kinds); got != want {
		t.Errorf("reload picked up %d ingesters, want %d", got, want)
	}

	// a pointer to that pointer is not a struct and must be refused rather than quietly
	// loading nothing
	if err = dm.Load(&newCfg); err == nil {
		t.Error(`Load should reject a **cfgType`)
	}
}
