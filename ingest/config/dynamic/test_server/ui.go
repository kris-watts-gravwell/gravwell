/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
	"github.com/gravwell/gravwell/v4/ingest/log"
)

//go:embed assets
var assets embed.FS

// refreshLists tells the page which fragments to re-fetch after a change, the small
// stand in for htmx in assets/hx.js acts on it the way real htmx acts on HX-Trigger.
const refreshLists = `#kinds,#runners`

// UI serves the HTML interface.  Every response is a fragment of HTML rendered on the
// server, the page holds no state of its own.
type UI struct {
	api   *API
	store *Store
	lgr   *log.Logger
	tpl   *template.Template
}

func NewUI(api *API, store *Store, lgr *log.Logger) (u *UI, err error) {
	if lgr == nil {
		lgr = log.NewDiscardLogger()
	}
	var tpl *template.Template
	if tpl, err = template.ParseFS(assets, `assets/index.html`); err != nil {
		return nil, fmt.Errorf("failed to parse templates %w", err)
	}
	return &UI{api: api, store: store, lgr: lgr, tpl: tpl}, nil
}

// Register mounts every UI route on a mux.
func (u *UI) Register(mux *http.ServeMux) {
	// the stylesheet and the htmx stand in are embedded, so this serves with no network
	// access and no build step
	if static, err := fs.Sub(assets, `assets`); err == nil {
		mux.Handle(`GET /static/`, http.StripPrefix(`/static/`, http.FileServer(http.FS(static))))
	} else {
		u.lgr.Error("failed to mount static assets", log.KVErr(err))
	}
	mux.HandleFunc(`GET /{$}`, u.index)
	mux.HandleFunc(`GET /ui/kinds`, u.kinds)
	mux.HandleFunc(`GET /ui/runners`, u.runners)
	mux.HandleFunc(`GET /ui/status`, u.status)
	mux.HandleFunc(`GET /ui/ingesters`, u.ingesters)
	mux.HandleFunc(`GET /ui/listrow`, u.listRow)
	mux.HandleFunc(`GET /ui/new`, u.newRunner)
	mux.HandleFunc(`GET /ui/edit`, u.editRunner)
	mux.HandleFunc(`POST /ui/save`, u.save)
	mux.HandleFunc(`POST /ui/delete`, u.del)
}

// formView is what the form template renders from.
type formView struct {
	Kind     string
	Name     string
	UUID     string
	Existing bool
	Fields   []field
	Note     *note

	// Targets are the ingesters that registered this kind, the only ones worth pinning a
	// configuration of it to.  Selected marks the ones this runner is already pinned to.
	Targets []targetView
	// Classes are the classes that have been seen, plus any this runner already names
	// even if no ingester is currently carrying them.
	Classes []classView
	// OtherClasses holds classes typed in by hand that are not in the known list.
	OtherClasses string
}

type targetView struct {
	UUID     string
	Class    string
	Kinds    string
	Selected bool
	Known    bool // false when the runner names a UUID that never registered this kind
}

type classView struct {
	Name     string
	Selected bool
}

type note struct {
	Class string // ok or bad
	Text  string
}

func (u *UI) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set(`Content-Type`, `text/html; charset=utf-8`)
	if err := u.tpl.ExecuteTemplate(w, name, data); err != nil {
		u.lgr.Error("failed to render", log.KV("template", name), log.KVErr(err))
	}
}

// fail renders an error into wherever the caller was targeting.  A failed action shows up
// in the interface rather than as a status code nobody sees.
func (u *UI) fail(w http.ResponseWriter, err error) {
	w.Header().Set(`Content-Type`, `text/html; charset=utf-8`)
	u.render(w, `note`, note{Class: `bad`, Text: err.Error()})
}

func (u *UI) index(w http.ResponseWriter, r *http.Request) {
	u.render(w, `page`, nil)
}

func (u *UI) kinds(w http.ResponseWriter, r *http.Request) {
	kinds, err := u.store.Kinds()
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, `kinds`, kinds)
}

func (u *UI) runners(w http.ResponseWriter, r *http.Request) {
	runners, err := u.store.Runners()
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, `runners`, runners)
}

func (u *UI) status(w http.ResponseWriter, r *http.Request) {
	u.render(w, `status`, struct{ Connected int }{len(u.api.Connected())})
}

// ingesters renders the dropdown behind the connection chip: who is connected right now,
// with the UUID and class each one authenticated with.
func (u *UI) ingesters(w http.ResponseWriter, r *http.Request) {
	live := map[uuid.UUID]bool{}
	for _, s := range u.api.Connected() {
		live[s.ID()] = true
	}
	known, err := u.store.Ingesters()
	if err != nil {
		u.fail(w, err)
		return
	}
	type row struct {
		UUID      string
		Class     string
		Kinds     string
		Connected bool
		LastSeen  string
	}
	out := make([]row, 0, len(known))
	for _, ing := range known {
		out = append(out, row{
			UUID:      ing.UUID.String(),
			Class:     ing.Class,
			Kinds:     strings.Join(ing.Kinds, `, `),
			Connected: live[ing.UUID],
			LastSeen:  ing.LastSeen.Format(time.RFC3339),
		})
	}
	u.render(w, `ingesters`, out)
}

// listRow renders one empty input for a list control, which is what the + button appends.
//
// The name is echoed back into the field, and html/template escapes it, so there is
// nothing to smuggle through here.  A name that does not match a variable the prototype
// declares is ignored at save time anyway, the prototype is the schema.
func (u *UI) listRow(w http.ResponseWriter, r *http.Request) {
	u.render(w, `listrow`, r.URL.Query().Get(`name`))
}

// assignmentView builds the two pickers for a kind, marking whatever cur is already
// pinned to.
func (u *UI) assignmentView(kind string, cur *dynamic.RunnerDefinition) (targets []targetView, classes []classView, other string, err error) {
	var known []Ingester
	if known, err = u.store.KindIngesters(kind); err != nil {
		return
	}
	selectedIDs := map[uuid.UUID]bool{}
	selectedClasses := map[string]bool{}
	if cur != nil && cur.Assigned != nil {
		for _, id := range cur.Assigned.UUIDs {
			selectedIDs[id] = true
		}
		for _, c := range cur.Assigned.Classes {
			selectedClasses[c] = true
		}
	}

	seen := map[uuid.UUID]bool{}
	for _, ing := range known {
		seen[ing.UUID] = true
		targets = append(targets, targetView{
			UUID:     ing.UUID.String(),
			Class:    ing.Class,
			Kinds:    strings.Join(ing.Kinds, `, `),
			Selected: selectedIDs[ing.UUID],
			Known:    true,
		})
	}
	// a runner may name an ingester that has not registered this kind, usually because it
	// has not connected since the server was started.  Show it rather than silently
	// dropping it on the next save.
	for id := range selectedIDs {
		if !seen[id] {
			targets = append(targets, targetView{UUID: id.String(), Selected: true})
		}
	}

	var knownClasses []string
	if knownClasses, err = u.store.Classes(); err != nil {
		return
	}
	inList := map[string]bool{}
	for _, c := range knownClasses {
		inList[c] = true
		classes = append(classes, classView{Name: c, Selected: selectedClasses[c]})
	}
	// classes this runner names that nothing is currently carrying stay editable as text
	var extras []string
	for c := range selectedClasses {
		if !inList[c] {
			extras = append(extras, c)
		}
	}
	sort.Strings(extras)
	other = strings.Join(extras, `, `)
	return
}

// newRunner draws an empty form for a registered kind.
func (u *UI) newRunner(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get(`kind`)
	proto, err := u.store.Kind(kind)
	if err != nil {
		u.fail(w, err)
		return
	}
	targets, classes, other, err := u.assignmentView(proto.Kind, nil)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, `form`, formView{
		Kind:         proto.Kind,
		UUID:         uuid.New().String(), // a new runner gets its identity up front
		Fields:       fieldsFor(proto, nil),
		Targets:      targets,
		Classes:      classes,
		OtherClasses: other,
	})
}

// editRunner draws a form filled in from a configured runner.
func (u *UI) editRunner(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.URL.Query().Get(`uuid`))
	if err != nil {
		u.fail(w, fmt.Errorf("invalid runner id %w", err))
		return
	}
	cur, err := u.store.Runner(id)
	if err != nil {
		u.fail(w, err)
		return
	}
	proto, err := u.store.Kind(cur.Kind)
	if err != nil {
		// the runner outlived its registration, which happens when an ingester that
		// could run it is not connected.  Fall back to describing it from itself so the
		// operator can still see and edit what is stored.
		proto = cur
	}
	targets, classes, other, err := u.assignmentView(cur.Kind, &cur)
	if err != nil {
		u.fail(w, err)
		return
	}
	u.render(w, `form`, formView{
		Kind:         cur.Kind,
		Name:         cur.Name,
		UUID:         cur.UUID.String(),
		Existing:     true,
		Fields:       fieldsFor(proto, &cur),
		Targets:      targets,
		Classes:      classes,
		OtherClasses: other,
	})
}

// save creates or updates a runner and pushes it to whatever is connected.
func (u *UI) save(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		u.fail(w, err)
		return
	}
	kind := r.Form.Get(`kind`)
	name := strings.TrimSpace(r.Form.Get(`name`))
	if name == `` {
		u.fail(w, fmt.Errorf("a runner needs a name"))
		return
	}
	proto, err := u.store.Kind(kind)
	if err != nil {
		u.fail(w, err)
		return
	}
	id, err := uuid.Parse(r.Form.Get(`uuid`))
	if err != nil {
		u.fail(w, fmt.Errorf("invalid runner id %w", err))
		return
	}
	// the runner as it stands, if any.  A secret that the form left blank keeps whatever
	// is already stored, because the form never showed it in the first place.
	var cur *dynamic.RunnerDefinition
	if existing, lerr := u.store.Runner(id); lerr == nil {
		cur = &existing
	}

	// the prototype is the schema, never the form, so a submitted field that the
	// ingester never advertised cannot become part of the config
	vars, err := parseForm(proto, r.Form, cur)
	if err != nil {
		u.fail(w, err)
		return
	}
	assigned, err := parseAssignment(r.Form)
	if err != nil {
		u.fail(w, err)
		return
	}
	rd := dynamic.RunnerDefinition{
		Kind:      proto.Kind,
		Name:      name,
		UUID:      id,
		Singleton: proto.Singleton,
		Variables: vars,
		Assigned:  assigned,
	}
	// render it the way the ingester will, so a config that cannot be written as an INI
	// is refused here rather than failing at the far end
	if _, err = rd.INI(); err != nil {
		u.fail(w, fmt.Errorf("this configuration cannot be represented: %w", err))
		return
	}
	if err = u.store.PutRunner(rd); err != nil {
		u.fail(w, err)
		return
	}

	delivered, pushErrs := u.api.push(rd)
	n := &note{Class: `ok`}
	switch {
	case len(pushErrs) > 0:
		n.Class = `bad`
		n.Text = fmt.Sprintf("Saved, but %d ingester(s) rejected it: %s",
			len(pushErrs), strings.Join(pushErrs, `; `))
	case delivered > 0:
		n.Text = fmt.Sprintf("Saved and pushed to %d connected ingester(s).", delivered)
	default:
		n.Text = `Saved. No ingester is connected, so nothing was pushed.`
	}

	targets, classes, other, err := u.assignmentView(rd.Kind, &rd)
	if err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set(`X-Refresh`, refreshLists)
	u.render(w, `form`, formView{
		Kind:         rd.Kind,
		Name:         rd.Name,
		UUID:         rd.UUID.String(),
		Existing:     true,
		Fields:       fieldsFor(proto, &rd),
		Targets:      targets,
		Classes:      classes,
		OtherClasses: other,
		Note:         n,
	})
}

// parseAssignment builds the assignment from the two pickers plus the free text box.
//
// An empty selection means unassigned, which is how a configuration goes to everything
// that can run it.  Nothing here trusts the browser for anything but the values
// themselves: a UUID that does not parse is refused rather than quietly dropped, because
// silently widening who receives a configuration is the wrong way to fail.
func parseAssignment(form url.Values) (a *dynamic.Assignment, err error) {
	out := &dynamic.Assignment{}
	for _, raw := range form[`assign.uuid`] {
		raw = strings.TrimSpace(raw)
		if raw == `` {
			continue
		}
		var id uuid.UUID
		if id, err = uuid.Parse(raw); err != nil {
			return nil, fmt.Errorf("invalid assignment UUID %q %w", raw, err)
		}
		if !containsUUID(out.UUIDs, id) {
			out.UUIDs = append(out.UUIDs, id)
		}
	}
	for _, c := range form[`assign.class`] {
		if c = strings.TrimSpace(c); c != `` && !containsString(out.Classes, c) {
			out.Classes = append(out.Classes, c)
		}
	}
	// classes typed by hand, for one that no ingester is carrying yet
	for _, c := range strings.FieldsFunc(form.Get(`assign.otherclasses`), func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if c = strings.TrimSpace(c); c != `` && !containsString(out.Classes, c) {
			out.Classes = append(out.Classes, c)
		}
	}
	sort.Strings(out.Classes)
	if out.Empty() {
		return nil, nil // unassigned, do not carry an empty struct around
	}
	return out, nil
}

func containsUUID(set []uuid.UUID, v uuid.UUID) bool {
	for _, cur := range set {
		if cur == v {
			return true
		}
	}
	return false
}

func containsString(set []string, v string) bool {
	for _, cur := range set {
		if cur == v {
			return true
		}
	}
	return false
}

// del removes a configured runner.
func (u *UI) del(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.URL.Query().Get(`uuid`))
	if err != nil {
		u.fail(w, fmt.Errorf("invalid runner id %w", err))
		return
	}
	if err = u.store.DeleteRunner(id); err != nil {
		u.fail(w, err)
		return
	}
	w.Header().Set(`X-Refresh`, refreshLists)
	u.render(w, `note`, note{Class: `ok`, Text: `Runner deleted.`})
}
