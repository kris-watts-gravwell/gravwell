/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package okta

import (
	_ "embed" // for the //go:embed directive below, nothing from it is referenced

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"
)

// DocLinks are the places to read about this plugin, shown next to it in a configuration
// interface.  Nothing here is load bearing, a link that goes stale costs a click.
var DocLinks = []dynamic.DocLink{
	{Name: `source`, Link: `https://github.com/gravwell/gravwell/tree/main/hosted/plugins/okta`},
}

// Icon is this plugin's artwork, read out of okta.svg at build time.
//
// It is third party brand artwork and ICON_LICENSE.md beside it records where it came
// from, what was changed, and the terms it is carried under.  That record names the
// file's checksum, so the file is embedded verbatim and must not be rewritten by anything
// that does not also update the record.
//
//go:embed okta.svg
var Icon string

// RunnerMetadata implements the optional dynamic.MetadataProvider interface, which is how
// this plugin's icon, documentation and version reach a webserver: the config type is
// what gets registered, so hanging the description off it means nothing has to be
// threaded through the registration path by hand.
func (c *Config) RunnerMetadata() *dynamic.RunnerMetadata {
	return &dynamic.RunnerMetadata{
		Icon:          Icon,
		Documentation: DocLinks,
		Version:       dynamic.Version(Version),
	}
}
