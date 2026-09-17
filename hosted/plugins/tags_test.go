/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package plugins

import (
	"reflect"
	"strings"
	"testing"

	"github.com/gravwell/gravwell/v4/hosted"
	"github.com/gravwell/gravwell/v4/hosted/plugins/jamf"
	"github.com/gravwell/gravwell/v4/hosted/plugins/mimecast"
	"github.com/gravwell/gravwell/v4/hosted/plugins/msgraph"
	"github.com/gravwell/gravwell/v4/hosted/plugins/sqs"
	"github.com/gravwell/gravwell/v4/hosted/plugins/tester"
	"github.com/gravwell/gravwell/v4/hosted/plugins/wiz"
	"github.com/gravwell/gravwell/v4/ingest"
)

// badTags are the shapes a tag gets wrong in the field.  Every one of them is something
// the indexer refuses at negotiation, so an ingester configured with one comes up and
// then does not ingest, which is the failure this validation exists to turn into an error
// somebody can see.
var badTags = []struct{ name, tag string }{
	{`space`, `bad tag`},
	{`bang`, `bad!tag`},
	{`dot`, `bad.tag`},
	{`colon`, `bad:tag`},
	{`slash`, `bad/tag`},
	{`star`, `bad*tag`},
	{`dollar`, `bad$tag`},
	{`bracket`, `bad[tag]`},
	{`quote`, `bad"tag`},
	{`backtick`, "bad`tag"},
	{`backslash`, `bad\tag`},
	{`equals`, `bad=tag`},
	{`percent`, `bad%tag`},
	{`tab`, "bad\ttag"},
	{`control`, "bad\x01tag"},
	// ingest.MAX_TAG_LENGTH is the limit and the check is strictly greater, so this is
	// the first length that is actually too long
	{`oversized`, strings.Repeat(`a`, ingest.MAX_TAG_LENGTH+1)},
}

// verifier is what every plugin config is, and what the dynamic validation path calls.
type verifier interface{ Verify() error }

// configs under test, each already valid apart from the tag the case puts in it.  The
// function shape is deliberate: each case needs a fresh config because Verify mutates.
type tagCase struct {
	kind string
	// withTag builds an otherwise valid config carrying tag
	withTag func(tag string) verifier
}

func tagCases() []tagCase {
	const uuidStr = `4f1c35f6-6af6-4103-8fdc-df2c63026f0d`
	return []tagCase{
		{`Tester`, func(tag string) verifier {
			return &tester.Config{
				BaseConfig:      hosted.BaseConfig{Ingester_UUID: uuidStr},
				SingleTagConfig: hosted.SingleTagConfig{Tag_Name: tag},
			}
		}},
		{`Jamf`, func(tag string) verifier {
			return &jamf.Config{
				BaseConfig:      hosted.BaseConfig{Ingester_UUID: uuidStr},
				SingleTagConfig: hosted.SingleTagConfig{Tag_Name: tag},
				Host:            `https://example.jamfcloud.com`,
				Client_Id:       `id`,
				Client_Secret:   `secret`,
			}
		}},
		{`SQS`, func(tag string) verifier {
			return &sqs.Config{
				BaseConfig:       hosted.BaseConfig{Ingester_UUID: uuidStr},
				SingleTagConfig:  hosted.SingleTagConfig{Tag_Name: tag},
				Queue_URL:        `https://sqs.us-east-1.amazonaws.com/1/q`,
				Region:           `us-east-1`,
				Credentials_Type: `static`,
				AKID:             `akid`,
				Secret:           `secret`,
			}
		}},
		{`Mimecast`, func(tag string) verifier {
			return &mimecast.Config{
				BaseConfig:     hosted.BaseConfig{Ingester_UUID: uuidStr},
				MultiTagConfig: hosted.MultiTagConfig{Tag_Name: tag},
				Client_Id:      `id`,
				Client_Secret:  `secret`,
				Api:            []mimecast.Api{mimecast.AuditApi},
			}
		}},
		{`MSGraph`, func(tag string) verifier {
			return &msgraph.Config{
				BaseConfig:    hosted.BaseConfig{Ingester_UUID: uuidStr},
				Tenant_ID:     `tenant`,
				Client_ID:     `id`,
				Client_Secret: `secret`,
				Content_Type:  []msgraph.ContentType{msgraph.ContentAlerts},
				Tag_Name:      tag,
			}
		}},
		{`Wiz`, func(tag string) verifier {
			return &wiz.Config{
				BaseConfig:    hosted.BaseConfig{Ingester_UUID: uuidStr},
				Client_Id:     `id`,
				Client_Secret: `secret`,
				Endpoint:      `https://api.us1.app.wiz.io/graphql`,
				Tag_Name:      tag,
			}
		}},
	}
}

// TestPluginsRejectBadTags is the gap this closes.  Before it, only wiz checked its tag,
// so a configuration naming a tag the indexer will refuse was accepted by every other
// plugin and only failed later, at negotiation, where nothing ties it back to the
// configuration that caused it.
func TestPluginsRejectBadTags(t *testing.T) {
	for _, tc := range tagCases() {
		t.Run(tc.kind, func(t *testing.T) {
			// the control: a good tag is still accepted, a check that rejects
			// everything is no use to anyone
			if err := tc.withTag(`good-tag`).Verify(); err != nil {
				t.Fatalf("a valid tag was rejected: %v", err)
			}
			for _, bt := range badTags {
				t.Run(bt.name, func(t *testing.T) {
					err := tc.withTag(bt.tag).Verify()
					if err == nil {
						t.Fatalf("accepted tag %q, which the indexer will refuse", bt.tag)
					}
					// the message has to say it is about a tag, an operator reads this
					// in a status list with no other context.  Case insensitive: the
					// plugins spell it Tag-Name, ingest spells it "Tag name".
					if !strings.Contains(strings.ToLower(err.Error()), `tag`) {
						t.Errorf("error does not mention the tag: %v", err)
					}
				})
			}
		})
	}
}

// TestTagPrefixIsCheckedAsAResolvedTag covers the case checking the raw fields would
// miss: a prefix is not a tag, it is half of one, and it is the join that goes wrong.
func TestTagPrefixIsCheckedAsAResolvedTag(t *testing.T) {
	const uuidStr = `4f1c35f6-6af6-4103-8fdc-df2c63026f0d`
	c := &mimecast.Config{
		BaseConfig:     hosted.BaseConfig{Ingester_UUID: uuidStr},
		MultiTagConfig: hosted.MultiTagConfig{Tag_Prefix: `bad prefix`},
		Client_Id:      `id`,
		Client_Secret:  `secret`,
		Api:            []mimecast.Api{mimecast.AuditApi},
	}
	if err := c.Verify(); err == nil {
		t.Error(`a Tag-Prefix that resolves to an illegal tag was accepted`)
	}
	// and a good prefix still resolves to something legal
	c = &mimecast.Config{
		BaseConfig:     hosted.BaseConfig{Ingester_UUID: uuidStr},
		MultiTagConfig: hosted.MultiTagConfig{Tag_Prefix: `mc`},
		Client_Id:      `id`,
		Client_Secret:  `secret`,
		Api:            []mimecast.Api{mimecast.AuditApi},
	}
	if err := c.Verify(); err != nil {
		t.Errorf("a valid Tag-Prefix was rejected: %v", err)
	}
	for _, tag := range c.Tags() {
		if err := ingest.CheckTag(tag); err != nil {
			t.Errorf("resolved tag %q is not valid: %v", tag, err)
		}
	}
}

// TestEveryTagCarryingPluginIsCovered keeps this honest as plugins are added: any config
// with a tag field has to implement TagProvider, or VerifyTags never sees it.
func TestEveryTagCarryingPluginIsCovered(t *testing.T) {
	kinds, err := Kinds()
	if err != nil {
		t.Fatal(err)
	}
	for _, pk := range kinds {
		t.Run(pk.Kind, func(t *testing.T) {
			rt := reflect.TypeOf(pk.Config)
			if !hasTagField(rt, 0) {
				t.Skip(`no tag field, nothing to validate`)
			}
			if _, ok := reflect.New(rt).Interface().(hosted.TagProvider); !ok {
				t.Errorf(`config has a tag field but does not implement hosted.TagProvider, so its tags are never checked`)
			}
		})
	}
}

// hasTagField reports whether a config carries a member whose name mentions a tag,
// walking embedded structs the way gcfg does.
func hasTagField(rt reflect.Type, depth int) bool {
	if depth > 4 || rt == nil || rt.Kind() != reflect.Struct {
		return false
	}
	for i := range rt.NumField() {
		f := rt.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			if hasTagField(f.Type, depth+1) {
				return true
			}
			continue
		}
		if f.IsExported() && strings.Contains(strings.ToLower(f.Name), `tag`) {
			return true
		}
	}
	return false
}
