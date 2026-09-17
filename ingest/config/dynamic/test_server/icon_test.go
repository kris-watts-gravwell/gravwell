/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
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

// TestSanitizeIconSuppliesAViewBox covers an icon that ships no viewBox: once its width
// and height are stripped it would scale unpredictably, so one is supplied.
func TestSanitizeIconSuppliesAViewBox(t *testing.T) {
	out, ok := sanitizeIcon(`<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48"><path d="M1 1 L2 2"/></svg>`)
	if !ok {
		t.Fatal(`a viewBox-less icon was dropped`)
	}
	if !strings.Contains(string(out), `viewBox="`+iconViewBox+`"`) {
		t.Errorf("no fallback viewBox:\n%s", out)
	}
}
