/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"
	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config"
	"github.com/gravwell/gravwell/v4/ingest/config/dynamic/rpc"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

var (
	ErrSingletonRegistered = errors.New("singleton runner of the same type already registered")
	ErrUnknownKind         = errors.New("runner kind unsupported, no registration kind found")
	ErrKindRegistered      = errors.New("runner kind already registered")
	ErrRunnerRegistered    = errors.New("runner of the same kind and name already registered")
	ErrInvalidRunner       = errors.New("runner configuration is not usable")
)

// validationSection is the INI section name a definition is rendered under when it is
// being checked rather than written.
//
// A fixed name is used rather than the real kind because the kind has to become a Go
// struct field to be parsed back, and a kind is a free form string: "okta" is a perfectly
// legal kind and not a legal exported field name.  The section header is the one part of
// the rendering that validation does not need to be faithful about, everything that can
// actually be wrong lives in the keys below it.
const validationSection = `Runner`

type Manager interface {
	Start() error
	Close() error
	Signal() <-chan struct{}                             // read only channel
	Load(any) error                                      // load files from disk and apply them to the config object
	RegisterKind(string, bool, any) error                // register a kind that we can run
	RegisterRunner(string, string, uuid.UUID, any) error // register a configured runner
}

type configuredRunner struct {
	RunnerDefinition
	backingFile string // path to backing file
	remote      bool   // came from the webserver rather than from this ingester
}

// registeredKind is a kind registration together with the Go type it was derived from.
//
// The type is the whole point.  A definition that arrives from a webserver has been
// through JSON and describes itself only as strings, ints and bools, which is not enough
// to know whether the plugin can actually run it.  Keeping the type means an incoming
// definition can be rendered, parsed back with the same loader the ingester uses at
// startup, and handed to the plugin's own Verify, which is the only thing that knows an
// Interval of "3" is not a duration.
type registeredKind struct {
	RunnerDefinition
	typ reflect.Type // the plugin's config struct, never a pointer
}

type NopManager struct {
	// mtx guards the two lists.  A DynamicConfigManager syncs from a background
	// goroutine while the ingester is still registering from its main thread, so these
	// are genuinely shared.  The fields stay exported because callers read them, but a
	// caller reading them directly while a sync is running is on its own, use the
	// accessors.
	mtx sync.Mutex

	// Available is the list of definitions that have been registered, only one entry per kind
	Available []registeredKind

	// Configured is a complete list of configured runners, each item must contain a fully
	// Populated Variable block
	Configured []configuredRunner

	running bool
}

func NewNil() Manager {
	return &NopManager{}
}

func (n *NopManager) Start() (err error) {
	n.mtx.Lock()
	if n.running {
		err = errors.New("already running")
	} else {
		n.running = true
	}
	n.mtx.Unlock()
	return
}

// Close always succeeds
func (n *NopManager) Close() error {
	n.mtx.Lock()
	n.running = false
	n.mtx.Unlock()
	return nil
}

// Signal returns a nil channel that will never fire
func (n *NopManager) Signal() (v <-chan struct{}) {
	return
}

// Load will actually load configuration blobs into the object passed in
// NopManager just validates that something sane was passed in
func (n *NopManager) Load(v any) (err error) {
	if v == nil {
		return errors.New("nil object")
	} else if reflect.ValueOf(v).Kind() != reflect.Pointer {
		return errors.New("object must be a pointer")
	}
	// check that we can write to the pointer
	return nil // all good
}

// RegisterKind takes a type and populates it as a something that this ingester CAN handle
// this function does not take any data in the type, it simply enumerates it into a RunnerDefinition
// without the Name and UUID and populates the Available list
func (n *NopManager) RegisterKind(kind string, singleton bool, v any) (err error) {
	if kind == `` {
		err = errors.New("missing kind")
		return
	} else if v == nil {
		err = errors.New("nil object")
		return
	}

	// MapRunnerDefinition insists on a name, a kind registration does not have one yet,
	// so hand it the kind and strip the name back out
	var rd RunnerDefinition
	if rd, err = MapRunnerDefinition(kind, kind, v); err != nil {
		return
	}
	// a kind describes what we can run, it carries no identity of its own
	rd.Name, rd.UUID = ``, uuid.Nil()
	rd.Singleton = singleton

	n.mtx.Lock()
	defer n.mtx.Unlock()
	// only one entry per kind
	if _, ok := n.lookupKindLocked(rd.Kind); ok {
		err = fmt.Errorf("%w %q", ErrKindRegistered, rd.Kind)
		return
	}
	n.Available = append(n.Available, registeredKind{RunnerDefinition: rd, typ: derefType(reflect.TypeOf(v))})
	return
}

// Validate reports whether a definition is one this ingester could actually run.
//
// It is the same path the configuration takes for real, which is what makes the answer
// worth anything: the definition is rendered to an INI block, parsed back with the loader
// the ingester uses at startup, and handed to the plugin's own Verify.  That catches the
// three separate ways a definition can be wrong, and they are genuinely separate: a value
// of the wrong type never parses, a key the plugin does not have never stores, and a value
// that is fine as a string but meaningless to the plugin is only ever caught by Verify.
//
// A nil return means every one of those passed.
func (n *NopManager) Validate(rd RunnerDefinition) error {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	return n.validateLocked(rd)
}

// validateLocked is Validate with the lock already held.
func (n *NopManager) validateLocked(rd RunnerDefinition) (err error) {
	var rk registeredKind
	var ok bool
	if rk, ok = n.lookupKindLocked(rd.Kind); !ok {
		return fmt.Errorf("%w %q", ErrUnknownKind, rd.Kind)
	} else if rk.typ == nil || rk.typ.Kind() != reflect.Struct {
		// a kind registered from something that is not a struct cannot be checked, and
		// refusing to run it on those grounds would be worse than running it
		return nil
	}

	// render under the substitute section name, see validationSection
	probe := rd
	probe.Kind = validationSection
	if probe.Name == `` {
		probe.Name = validationSection // INI insists on a name, this one is thrown away
	}
	var blob string
	if blob, err = probe.INI(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRunner, err)
	}

	// struct{ Runner map[string]*T }, which is the shape gcfg maps a named section onto
	holder := reflect.New(reflect.StructOf([]reflect.StructField{{
		Name: validationSection,
		Type: reflect.MapOf(reflect.TypeFor[string](), reflect.PointerTo(rk.typ)),
	}}))
	if err = config.LoadConfigBytes(holder.Interface(), []byte(blob)); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRunner, err)
	}

	// exactly one subsection went in, so exactly one should have come back out
	set := holder.Elem().Field(0)
	if set.Len() != 1 {
		return fmt.Errorf("%w: produced %d configurations, want 1", ErrInvalidRunner, set.Len())
	}
	for _, k := range set.MapKeys() {
		cur := set.MapIndex(k)
		if cur.IsNil() {
			return fmt.Errorf("%w: produced no configuration", ErrInvalidRunner)
		}
		// Verify is optional, a plugin that does not have one has nothing further to say
		if v, ok := cur.Interface().(interface{ Verify() error }); ok {
			if err = v.Verify(); err != nil {
				return fmt.Errorf("%w: %w", ErrInvalidRunner, err)
			}
		}
	}
	return nil
}

// Kinds returns a copy of the registered kinds, safe to read while a sync is running.
func (n *NopManager) Kinds() (r []RunnerDefinition) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	r = make([]RunnerDefinition, 0, len(n.Available))
	for _, rk := range n.Available {
		r = append(r, rk.RunnerDefinition)
	}
	return
}

// KindNames returns just the names of the registered kinds.
func (n *NopManager) KindNames() (r []string) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	r = make([]string, 0, len(n.Available))
	for _, rd := range n.Available {
		r = append(r, rd.Kind)
	}
	return
}

// lookupKind finds the registration for a kind.
func (n *NopManager) lookupKind(kind string) (rk registeredKind, ok bool) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	return n.lookupKindLocked(kind)
}

// lookupKindLocked is lookupKind with the lock already held.  The Available list carries
// at most one entry per kind so the first hit is the only hit.
func (n *NopManager) lookupKindLocked(kind string) (rk registeredKind, ok bool) {
	for _, cur := range n.Available {
		if cur.Kind == kind {
			rk, ok = cur, true
			return
		}
	}
	return
}

// RegisterRunner registers a complete runner with a given name, kind, UUID, and fully populated config block
// If the UUID is empty one is generated, the kind is validated agains the Available list, if no
// available registration is present RegisterRunner rejects the registration.
// If the registered kind is marked as a singleton and an existing Kind already exists
// the registration is rejected.  Upon successful registration the the complete RunnerDefinition is
// translated to a INI block and written to a .conf file in the storage directory
func (n *NopManager) RegisterRunner(name, kind string, guid uuid.UUID, v any) (err error) {
	n.mtx.Lock()
	defer n.mtx.Unlock()
	var rd RunnerDefinition
	if rd, _, err = n.prepareRunnerLocked(name, kind, guid, v); err == nil {
		// the NopManager has nowhere to put the INI block, it just remembers the runner
		n.Configured = append(n.Configured, configuredRunner{RunnerDefinition: rd})
	}
	return
}

// prepareRunner validates a registration against the current state and resolves it into a
// complete definition plus the INI block it renders to.  It deliberately records nothing,
// a manager with storage has to get the config onto disk before it can claim the runner is
// registered, so the caller owns the append to Configured.
func (n *NopManager) prepareRunnerLocked(name, kind string, guid uuid.UUID, v any) (rd RunnerDefinition, ini string, err error) {
	if name == `` {
		err = errors.New("missing name")
		return
	} else if kind == `` {
		err = errors.New("missing kind")
		return
	} else if v == nil {
		err = errors.New("nil object")
		return
	}
	// the kind has to be one we advertised, we cannot run something we do not know about
	var def registeredKind
	var ok bool
	if def, ok = n.lookupKindLocked(kind); !ok {
		err = fmt.Errorf("%w %q", ErrUnknownKind, kind)
		return
	}

	if rd, err = MapRunnerDefinition(kind, name, v); err != nil {
		return
	}

	// a singleton kind can only be configured once, everything else just has to be
	// uniquely named within its kind
	for _, cur := range n.Configured {
		if cur.Kind != rd.Kind {
			continue
		} else if def.Singleton {
			err = fmt.Errorf("%w %q", ErrSingletonRegistered, rd.Kind)
			return
		} else if cur.Name == rd.Name {
			err = fmt.Errorf("%w %q %q", ErrRunnerRegistered, rd.Kind, rd.Name)
			return
		}
	}

	// an explicitly provided UUID wins, then one lifted out of the config, then a new one
	if guid != uuid.Nil() {
		rd.UUID = guid
	} else if rd.UUID == uuid.Nil() {
		rd.UUID = uuid.New()
	}
	rd.Singleton = def.Singleton

	// render it now, a config that cannot be represented in an INI block is rejected
	// here rather than at write time so that every manager fails the same way
	ini, err = rd.INI()
	return
}

type DynamicConfigManager struct {
	NopManager //TODO FIXME
	Config     // embed the config
	guid       uuid.UUID
	lgr        *log.Logger
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup

	// ch carries the reload signal to whoever is driving the ingester.  It is buffered
	// by one, a burst of changes should be one reload.
	ch chan struct{}

	// nudge wakes the background client when something changed locally, so a kind
	// registered after we connected is declared without waiting for the poll.
	nudge chan struct{}

	// handlers are the methods a webserver may call on us, principally a config push.
	handlers *rpc.Mux

	// pollInterval is how often we ask for our configuration, a field rather than the
	// constant so that tests do not have to wait on it.
	pollInterval time.Duration

	// statuses is the verdict on every configuration the webserver has handed us, as of
	// the last sync.  It is guarded by the embedded NopManager's mutex along with the
	// lists it is derived from, because it has to be rebuilt in the same critical section
	// that decides what is on disk or the two can disagree.
	statuses []RunnerStatus
}

func NewDynamicConfigManager(ctx context.Context, c Config, guid uuid.UUID, lgr *log.Logger) (m Manager, err error) {
	if err = c.Verify(); err != nil {
		return
	}
	if ctx == nil {
		ctx = context.TODO()
	}
	if lgr == nil {
		lgr = log.NewDiscardLogger()
	}

	// validate the local config

	dcm := &DynamicConfigManager{
		Config:       c,
		guid:         guid,
		lgr:          lgr,
		ch:           make(chan struct{}, 1),
		nudge:        make(chan struct{}, 1),
		pollInterval: c.PollInterval(),
	}
	// our own cancellable view of the caller's context, so Close stops the background
	// client without the caller having to cancel anything
	dcm.ctx, dcm.cancel = context.WithCancel(ctx)

	// a webserver may push a config rather than waiting for us to poll
	dcm.handlers = rpc.NewMux()
	if err = dcm.handlers.Register(MethodApplyConfig, func(c context.Context, params json.RawMessage) (any, error) {
		return dcm.applyConfig(c, params)
	}); err != nil {
		dcm.cancel()
		return nil, err
	}

	dcm.start()
	m = dcm
	return
}

// Signal returns the channel that fires when the configuration on disk has changed.  The
// caller should reload its configuration and hand the result back to Load.
func (dcm *DynamicConfigManager) Signal() <-chan struct{} {
	return dcm.ch
}

// Close stops the background client and waits for it to finish.
func (dcm *DynamicConfigManager) Close() error {
	dcm.cancel()
	dcm.wg.Wait()
	return nil
}

func (dcm *DynamicConfigManager) Start() (err error) {
	dcm.mtx.Lock()
	if dcm.running {
		err = errors.New("already running")
	} else {
		dcm.start()
		dcm.running = true
	}
	dcm.mtx.Unlock()
	return
}

// RegisterKind registers a kind and wakes the background client so that a kind registered
// after we connected is declared without waiting for the next poll.
func (dcm *DynamicConfigManager) RegisterKind(kind string, singleton bool, v any) (err error) {
	if err = dcm.NopManager.RegisterKind(kind, singleton, v); err != nil {
		return
	}
	select {
	case dcm.nudge <- struct{}{}:
	default:
	}
	return
}

func (dcm *DynamicConfigManager) Load(v any) (err error) {
	// use the embedded NopManager to do the any object validation because we are lazy
	if err = dcm.NopManager.Load(v); err != nil {
		return
	}

	// use the config system to load overlays
	err = config.LoadConfigOverlays(v, dcm.Storage)
	return
}

func (dcm *DynamicConfigManager) RegisterRunner(name, kind string, guid uuid.UUID, v any) (err error) {
	dcm.mtx.Lock()
	defer dcm.mtx.Unlock()
	var rd RunnerDefinition
	var iniContent string
	if rd, iniContent, err = dcm.prepareRunnerLocked(name, kind, guid, v); err != nil {
		return
	}
	// generate filename as kind_name_guid.conf, use the resolved UUID rather than the one
	// handed in, prepareRunner may have generated it or lifted it out of the config
	fname := fmt.Sprintf("%s_%s_%v.conf", fnameChunk(kind), fnameChunk(name), rd.UUID)
	pth := filepath.Join(dcm.Storage, fname)
	if err = writeConfFile(pth, iniContent); err != nil {
		return
	}
	// only claim the runner once its config is actually on disk.  These are runners the
	// ingester itself reported, so they are not remote and a server that does not know
	// about them must not cause them to be deleted.
	dcm.Configured = append(dcm.Configured, configuredRunner{RunnerDefinition: rd, backingFile: pth})
	return
}

// writeConfFile writes an INI block to pth by way of a temporary file so that a partially
// written config is never visible to a loader.  The temporary file must not end in .conf,
// overlay loading consumes anything in the storage directory that does.
func writeConfFile(pth, ini string) (err error) {
	tmp := pth + `.temp`
	var fout *os.File
	if fout, err = os.OpenFile(tmp, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0660); err != nil {
		return
	}
	if _, err = io.WriteString(fout, ini); err != nil {
		fout.Close()
	} else if err = fout.Close(); err == nil {
		if err = os.Rename(tmp, pth); err == nil {
			return // the only path that leaves a file behind
		}
	}
	os.Remove(tmp) // best effort, we are already returning an error
	return
}

// fnameChunk is just a helper that removes spaces, non-printable characters, and any characters that
// cannot be part of a file name or may traditionally be part of a file path (like /, \, etc...)
func fnameChunk(v string) (r string) {
	// an allow list rather than a block list, the set of characters that are safe in a
	// file name on every platform we ship to is far smaller and far more stable than the
	// set that is not.  A dot is excluded too, it is what makes .. and an extension
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_':
		default:
			return -1 // drop it
		}
		return r
	}, v)
}
