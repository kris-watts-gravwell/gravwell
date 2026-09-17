/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"encoding/xml"
	"html"
	"html/template"
	"io"
	"strings"
)

// An icon is drawn by whoever wrote the ingester, and it arrives here over the wire.  It
// is therefore untrusted markup, and SVG is a document format rather than an image one: it
// can carry <script>, event handler attributes, <foreignObject> holding arbitrary HTML,
// and references out to other origins.  Inlining one as it arrived would let any ingester
// holding the shared token run script in the browser of whoever opens this page, which is
// a straight line from "can ingest" to "can drive the webserver interface".
//
// So nothing is inlined as it arrived.  The markup is parsed and rebuilt from an allow
// list: an element not named here does not survive, an attribute not named here does not
// survive, and anything that fails to parse is dropped entirely rather than patched up.
// An allow list is the only shape that is safe to be wrong about, because the failure of
// a list of known-bad things is to let something through, and the failure of this is a
// plugin that draws no icon.
const (
	// maxIconBytes caps what will be parsed at all.  An icon is a few hundred bytes of
	// path data, and a page drawing a list of them should not be able to be made
	// enormous by an ingester that claims otherwise.
	maxIconBytes = 32 * 1024

	// iconViewBox is used when a plugin ships an icon with no viewBox of its own, which
	// would otherwise scale unpredictably once the width and height are stripped.
	iconViewBox = `0 0 24 24`
)

// iconElements is every element that may appear.  Shapes and grouping only: no <script>,
// no <foreignObject>, no <image> or <use> (both reference other documents), no <style>
// (a stylesheet can reach out with url()), and no animation elements.
var iconElements = map[string]bool{
	`svg`: true, `g`: true, `title`: true, `desc`: true,
	`path`: true, `circle`: true, `ellipse`: true, `line`: true,
	`polyline`: true, `polygon`: true, `rect`: true,
}

// iconAttrs is every attribute that may appear: geometry and presentation, nothing that
// can name a URL or carry code.  Notably absent are style, href and xlink:href, every
// on* handler, and id/aria-labelledby, which are dropped because a page draws many icons
// at once and duplicated ids break both the document and the references into it.
var iconAttrs = map[string]bool{
	`viewbox`: true, `transform`: true, `d`: true, `points`: true,
	`x`: true, `y`: true, `x1`: true, `y1`: true, `x2`: true, `y2`: true,
	`cx`: true, `cy`: true, `r`: true, `rx`: true, `ry`: true,
	`width`: true, `height`: true,
	`fill`: true, `fill-opacity`: true, `fill-rule`: true, `clip-rule`: true,
	`stroke`: true, `stroke-width`: true, `stroke-linecap`: true,
	`stroke-linejoin`: true, `stroke-dasharray`: true, `stroke-opacity`: true,
	`stroke-miterlimit`: true, `opacity`: true, `vector-effect`: true,
}

// rootOnlyAttrs are the attributes only the outermost <svg> may carry.  width and height
// are dropped everywhere else too, see sanitizeIcon.
var rootOnlyAttrs = map[string]bool{`viewbox`: true}

// sanitizeIcon rebuilds a plugin supplied SVG from the allow lists above.
//
// The second return says whether there is anything to draw.  An icon that is empty,
// oversized, not an SVG, or not well formed comes back as nothing at all and the caller
// falls back to drawing the plugin's name, which is the correct outcome for a decorative
// element: a broken icon is never worth a broken page.
//
// The root is re-emitted rather than copied.  Its width and height are dropped so that
// the stylesheet decides how big an icon is instead of whoever drew it, and it is marked
// aria-hidden because the name it sits next to is already the accessible label.
func sanitizeIcon(raw string) (template.HTML, bool) {
	if strings.TrimSpace(raw) == `` || len(raw) > maxIconBytes {
		return ``, false
	}
	dec := xml.NewDecoder(strings.NewReader(raw))
	// an entity a plugin invented is not something to go and resolve, and a reference to
	// an external one is a way out of this process
	dec.Strict = true
	dec.Entity = xml.HTMLEntity

	var sb strings.Builder
	var depth, skip int
	var shapes int
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		} else if err != nil {
			return ``, false // malformed, and a half parsed icon is not worth salvaging
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if skip > 0 {
				skip++ // already inside something disallowed, stay inside it
				continue
			}
			if !iconElements[name] {
				skip = 1
				continue
			}
			if depth == 0 {
				if name != `svg` {
					return ``, false // not an icon at all
				}
				sb.WriteString(`<svg xmlns="http://www.w3.org/2000/svg" class="icon" aria-hidden="true" focusable="false"`)
				writeIconAttrs(&sb, t.Attr, true)
				sb.WriteString(`>`)
				depth++
				continue
			}
			if name != `title` && name != `desc` && name != `g` {
				shapes++
			}
			sb.WriteString(`<` + name)
			writeIconAttrs(&sb, t.Attr, false)
			sb.WriteString(`>`)
			depth++
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if depth == 0 {
				continue
			}
			depth--
			sb.WriteString(`</` + strings.ToLower(t.Name.Local) + `>`)
		case xml.CharData:
			// text only survives inside a title or desc, and only as escaped text.
			// Everything else in an icon is attributes.
			if skip == 0 && depth > 0 {
				sb.WriteString(html.EscapeString(string(t)))
			}
		}
		// comments, processing instructions and directives are simply never emitted
	}
	if depth != 0 || shapes == 0 {
		return ``, false // unbalanced, or nothing left that would draw anything
	}
	return template.HTML(sb.String()), true
}

// writeIconAttrs emits the attributes an element is allowed to keep.
//
// Namespaced attributes are dropped wholesale: the only ones that turn up in practice are
// xlink:href and the provenance namespaces, and none of them belong in an icon.  The root
// keeps its viewBox but loses width and height so the stylesheet sizes it; a child keeps
// width and height because that is geometry on a <rect>.
func writeIconAttrs(sb *strings.Builder, attrs []xml.Attr, root bool) {
	var sawViewBox bool
	for _, a := range attrs {
		if a.Name.Space != `` {
			continue
		}
		name := strings.ToLower(a.Name.Local)
		if name == `xmlns` {
			continue // written by hand on the root, never carried over
		}
		if !iconAttrs[name] {
			continue
		}
		if root {
			if name == `width` || name == `height` {
				continue // the stylesheet decides, not the plugin
			}
		} else if rootOnlyAttrs[name] {
			continue
		}
		if name == `viewbox` {
			if !root {
				continue
			}
			sawViewBox = true
			sb.WriteString(` viewBox="` + html.EscapeString(a.Value) + `"`)
			continue
		}
		sb.WriteString(` ` + name + `="` + html.EscapeString(a.Value) + `"`)
	}
	if root && !sawViewBox {
		sb.WriteString(` viewBox="` + iconViewBox + `"`)
	}
}
