/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"html/template"
	"regexp"
	"strings"
	"testing"

	"github.com/gravwell/gravwell/v4/hosted/plugins/tester"
)

// TestSanitizeIconKeepsRealArtwork checks that the thing this exists to draw still draws.
// A sanitizer that drops everything is safe and useless.
func TestSanitizeIconKeepsRealArtwork(t *testing.T) {
	out, ok := sanitizeIcon(tester.Icon)
	if !ok {
		t.Fatal(`the tester plugin's own icon did not survive sanitizing`)
	}
	got := string(out)
	for _, must := range []string{
		`<svg `,
		`viewBox="0 0 24 24"`,
		`aria-hidden="true"`,
		`class="icon"`,
		`M9.8 8 V12.6`,        // the flask body path, verbatim
		`fill="currentColor"`, // so it follows the page's theme
		`</svg>`,
	} {
		if !strings.Contains(got, must) {
			t.Errorf("sanitized icon is missing %q\n%s", must, got)
		}
	}
	// the plugin shipped width and height, the stylesheet decides those now
	if strings.Contains(got, `width="24"`) || strings.Contains(got, `height="24"`) {
		t.Errorf("the root kept its own size:\n%s", got)
	}
	// five paths went in, five should come out
	if n := strings.Count(got, `<path`); n != 5 {
		t.Errorf("kept %d paths, want 5:\n%s", n, got)
	}
}

// TestSanitizeIconStripsHostileMarkup is the one that matters.  An icon is authored by
// whoever wrote the ingester and arrives over the wire, so every one of these is a way to
// run script in the browser of whoever opens the configuration page.
func TestSanitizeIconStripsHostileMarkup(t *testing.T) {
	const wrap = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24">%s<path d="M1 1 L2 2"/></svg>`
	hostile := []struct {
		name    string
		payload string
		banned  []string
	}{
		{`script element`, `<script>alert(1)</script>`, []string{`script`, `alert`}},
		{`event handler`, `<circle cx="1" cy="1" r="1" onload="alert(1)"/>`, []string{`onload`, `alert`}},
		{`click handler`, `<rect width="1" height="1" onclick="steal()"/>`, []string{`onclick`, `steal`}},
		{`foreignObject`, `<foreignObject><body xmlns="http://www.w3.org/1999/xhtml"><img src=x onerror=alert(1)></body></foreignObject>`, []string{`foreignObject`, `onerror`, `alert`}},
		{`external image`, `<image href="https://evil.example/x.png"/>`, []string{`image`, `evil.example`}},
		{`use reference`, `<use href="https://evil.example/x.svg#a"/>`, []string{`use href`, `evil.example`}},
		{`style element`, `<style>@import url(https://evil.example/x.css);</style>`, []string{`style`, `evil.example`}},
		{`style attribute`, `<circle cx="1" cy="1" r="1" style="background:url(https://evil.example/x)"/>`, []string{`style=`, `evil.example`}},
		{`animate`, `<animate attributeName="x" to="1" onbegin="alert(1)"/>`, []string{`animate`, `onbegin`}},
		{`set handler`, `<set attributeName="x" onend="alert(1)"/>`, []string{`onend`, `alert`}},
		{`nested script in group`, `<g><g><script>alert(1)</script></g></g>`, []string{`script`, `alert`}},
		{`xlink href`, `<a xlink:href="javascript:alert(1)" xmlns:xlink="http://www.w3.org/1999/xlink"><circle cx="1" cy="1" r="1"/></a>`, []string{`javascript:`, `xlink`}},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			out, ok := sanitizeIcon(strings.Replace(wrap, `%s`, tc.payload, 1))
			if !ok {
				return // dropped entirely, which is a perfectly good outcome
			}
			got := strings.ToLower(string(out))
			for _, banned := range tc.banned {
				if strings.Contains(got, strings.ToLower(banned)) {
					t.Errorf("%q survived sanitizing:\n%s", banned, out)
				}
			}
			// nothing that can carry code should ever appear, whatever the input
			for _, never := range []string{`<script`, `onload`, `onerror`, `onclick`, `javascript:`, `<foreignobject`, `<style`, `style=`} {
				if strings.Contains(got, never) {
					t.Errorf("%q survived sanitizing:\n%s", never, out)
				}
			}
		})
	}
}

// TestSanitizeIconRejectsJunk covers the inputs that are not icons at all.  A decorative
// element is never worth a broken page, so every one of these draws nothing.
func TestSanitizeIconRejectsJunk(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{`empty`, ``},
		{`whitespace`, "  \n\t "},
		{`not xml`, `this is not markup`},
		{`unbalanced`, `<svg viewBox="0 0 1 1"><path d="M1 1"`},
		{`not an svg`, `<html><body>hi</body></html>`},
		{`html masquerading`, `<div><script>alert(1)</script></div>`},
		{`no shapes`, `<svg viewBox="0 0 1 1"><title>nothing</title></svg>`},
		{`oversized`, `<svg viewBox="0 0 1 1"><path d="` + strings.Repeat(`M1 1 `, 20000) + `"/></svg>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if out, ok := sanitizeIcon(tc.in); ok {
				t.Errorf("accepted junk, produced %q", out)
			}
		})
	}
}

// TestSanitizeIconSuppliesAViewBox covers an icon that ships no viewBox.
//
// Its width and height are stripped so the stylesheet can size it, and without a viewBox
// there is then nothing left saying how far the drawing extends.  The size it was drawn
// at is that answer: assuming a square instead crops everything drawn at another size to
// its top left corner, which looks like a rendering fault rather than a missing attribute.
func TestSanitizeIconSuppliesAViewBox(t *testing.T) {
	out, ok := sanitizeIcon(`<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><path d="M1 1 L2 2"/></svg>`)
	if !ok {
		t.Fatal(`a viewBox-less icon was dropped`)
	}
	if !strings.Contains(string(out), `viewBox="0 0 48 48"`) {
		t.Errorf("the viewBox was not taken from the authored size:\n%s", out)
	}

	// a unit suffix is still a size
	if out, ok = sanitizeIcon(`<svg xmlns="http://www.w3.org/2000/svg" width="40px" height="30px"><path d="M1 1 L2 2"/></svg>`); !ok {
		t.Fatal(`dropped`)
	} else if !strings.Contains(string(out), `viewBox="0 0 40 30"`) {
		t.Errorf("a unit suffix was not understood:\n%s", out)
	}

	// and with nothing at all to go on, the square fallback stands
	if out, ok = sanitizeIcon(`<svg xmlns="http://www.w3.org/2000/svg"><path d="M1 1 L2 2"/></svg>`); !ok {
		t.Fatal(`dropped`)
	} else if !strings.Contains(string(out), `viewBox="`+iconViewBox+`"`) {
		t.Errorf("no fallback viewBox:\n%s", out)
	}
}

// TestSanitizeIconKeepsGradients covers the paint a full colour brand mark is made of.
// Keeping the shape and dropping the gradient it points at renders as nothing at all,
// which is worse than not drawing the icon.
func TestSanitizeIconKeepsGradients(t *testing.T) {
	const raw = `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">` +
		`<defs><linearGradient x1="0%" y1="100%" x2="100%" y2="0%" id="a">` +
		`<stop stop-color="#B0084D" offset="0%"/><stop stop-color="#FF4F8B" offset="100%"/>` +
		`</linearGradient></defs>` +
		`<g fill="none" fill-rule="evenodd"><path d="M0 0h40v40H0z" fill="url(#a)"/></g></svg>`
	out, ok := sanitizeIcon(raw)
	if !ok {
		t.Fatal(`a gradient filled icon was dropped entirely`)
	}
	got := string(out)
	// the camel case spelling has to survive: "lineargradient" is not a gradient
	if !strings.Contains(got, `<linearGradient `) {
		t.Errorf("the element name was not kept in its canonical spelling:\n%s", got)
	}
	for _, must := range []string{`<defs>`, `<stop `, `stop-color="#B0084D"`, `offset="0%"`} {
		if !strings.Contains(got, must) {
			t.Errorf("missing %q:\n%s", must, got)
		}
	}
	// and the reference still points at the gradient, under its rewritten name
	id := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(got)
	ref := regexp.MustCompile(`fill="url\(#([^)]+)\)"`).FindStringSubmatch(got)
	if id == nil || ref == nil {
		t.Fatalf("lost the gradient or the reference to it:\n%s", got)
	}
	if id[1] != ref[1] {
		t.Errorf("the reference %q does not match the gradient %q", ref[1], id[1])
	}
	if id[1] == `a` {
		t.Error(`the id was not namespaced, so two icons on one page would collide`)
	}
}

// TestSanitizeIconNamespacesIdsPerIcon is why the ids are rewritten at all.  Icons come
// from different plugins and were exported by the same tools, so they collide by default:
// "linearGradient-1" and "a" are what Sketch and svgo name the first gradient.
func TestSanitizeIconNamespacesIdsPerIcon(t *testing.T) {
	mk := func(color string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">` +
			`<defs><linearGradient id="a"><stop stop-color="` + color + `" offset="0%"/></linearGradient></defs>` +
			`<path d="M0 0h40v40H0z" fill="url(#a)"/></svg>`
	}
	first, ok := sanitizeIcon(mk(`#111111`))
	if !ok {
		t.Fatal(`dropped`)
	}
	second, ok := sanitizeIcon(mk(`#222222`))
	if !ok {
		t.Fatal(`dropped`)
	}
	idOf := func(h template.HTML) string {
		m := regexp.MustCompile(`id="([^"]+)"`).FindStringSubmatch(string(h))
		if m == nil {
			t.Fatalf("no id in %s", h)
		}
		return m[1]
	}
	if idOf(first) == idOf(second) {
		t.Errorf("two different icons share the id %q, so the second would paint itself with the first one's gradient", idOf(first))
	}
	// the same icon twice is the same markup, so a fragment rendered more than once on a
	// page is byte for byte identical
	again, _ := sanitizeIcon(mk(`#111111`))
	if string(again) != string(first) {
		t.Error(`sanitizing is not deterministic, the same icon produced different markup`)
	}
}

// TestSanitizeIconRefusesForeignPaint is the hole that keeping url() would open.  A paint
// server is fetched by the browser, so a reference out to another origin is a request the
// page makes on behalf of whoever wrote the icon.
func TestSanitizeIconRefusesForeignPaint(t *testing.T) {
	for _, tc := range []struct{ name, paint string }{
		{`remote document`, `url(https://evil.example/x.svg#a)`},
		{`protocol relative`, `url(//evil.example/x.svg#a)`},
		{`data uri`, `url(data:image/svg+xml;base64,AAAA)`},
		{`quoted remote`, `url('https://evil.example/x.svg#a')`},
		{`javascript`, `url(javascript:alert(1))`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">` +
				`<path d="M0 0h40v40H0z" fill="` + tc.paint + `"/></svg>`
			out, ok := sanitizeIcon(raw)
			if !ok {
				return // dropped entirely is a fine answer
			}
			got := strings.ToLower(string(out))
			for _, never := range []string{`evil.example`, `javascript:`, `data:`} {
				if strings.Contains(got, never) {
					t.Errorf("a foreign paint reference survived (%s):\n%s", never, out)
				}
			}
			// every url() that is left has to point into this icon, at an id this
			// sanitizer wrote.  The xmlns on the root is the only other "//" in here, so
			// checking the references themselves is what this has to look at.
			for _, m := range regexp.MustCompile(`url\(([^)]*)\)`).FindAllStringSubmatch(got, -1) {
				if !strings.HasPrefix(m[1], `#i`) {
					t.Errorf("a url() escaped the icon: %q\n%s", m[1], out)
				}
			}
		})
	}
}

// TestSanitizeIconDropsGradientHrefs covers the one attribute on a gradient that can
// reach another document.  It is namespaced, so it is already dropped with every other
// namespaced attribute, and this says that has to stay true.
func TestSanitizeIconDropsGradientHrefs(t *testing.T) {
	raw := `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="40" height="40">` +
		`<defs><linearGradient id="a" xlink:href="https://evil.example/g.svg#b"><stop stop-color="#fff" offset="0%"/></linearGradient></defs>` +
		`<path d="M0 0h40v40H0z" fill="url(#a)"/></svg>`
	out, ok := sanitizeIcon(raw)
	if !ok {
		return
	}
	got := strings.ToLower(string(out))
	if strings.Contains(got, `href`) || strings.Contains(got, `evil.example`) {
		t.Errorf("a gradient kept a reference to another document:\n%s", out)
	}
}

// TestSanitizeIconDropsUnusableIds keeps the id rewriting from having to think about
// escaping: an id that could never be the target of url(#id) is not worth keeping.
func TestSanitizeIconDropsUnusableIds(t *testing.T) {
	for _, bad := range []string{`a"onload="alert(1)`, `a b`, `a)`, `1abc`, ``} {
		raw := `<svg xmlns="http://www.w3.org/2000/svg" width="40" height="40">` +
			`<defs><linearGradient id="` + bad + `"><stop stop-color="#fff" offset="0%"/></linearGradient></defs>` +
			`<path d="M0 0h40v40H0z"/></svg>`
		out, ok := sanitizeIcon(raw)
		if !ok {
			continue
		}
		got := strings.ToLower(string(out))
		if strings.Contains(got, `onload`) || strings.Contains(got, `alert`) {
			t.Errorf("id %q smuggled something through:\n%s", bad, out)
		}
	}
}
