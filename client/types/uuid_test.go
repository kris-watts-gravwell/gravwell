/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package types

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"testing"
	"uuid"

	guuid "github.com/google/uuid"
)

// legacyThing mirrors Thing as it was when it used github.com/google/uuid.
type legacyThing struct {
	UUID     guuid.UUID
	UID      int32
	Contents []byte
}

// legacyWellData mirrors IndexerWellData as it was when it used github.com/google/uuid.
type legacyWellData struct {
	UUID       guuid.UUID
	Replicated map[guuid.UUID][]WellInfo
}

func gobRoundTrip(t *testing.T, in, out any) {
	t.Helper()
	var bb bytes.Buffer
	if err := gob.NewEncoder(&bb).Encode(in); err != nil {
		t.Fatal(err)
	}
	if err := gob.NewDecoder(&bb).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func TestUUIDGobCompatibleWithGoogleUUID(t *testing.T) {
	g := guuid.New()
	u := UUID(g)

	// old encoder, new decoder
	var thing Thing
	gobRoundTrip(t, legacyThing{UUID: g, UID: 7, Contents: []byte("x")}, &thing)
	if thing.UUID != u || thing.UID != 7 {
		t.Fatalf("legacy -> new mismatch: %v %d", thing.UUID, thing.UID)
	}

	// new encoder, old decoder
	var legacy legacyThing
	gobRoundTrip(t, Thing{UUID: u, UID: 9}, &legacy)
	if legacy.UUID != g || legacy.UID != 9 {
		t.Fatalf("new -> legacy mismatch: %v %d", legacy.UUID, legacy.UID)
	}

	// map keys
	var iwd IndexerWellData
	gobRoundTrip(t, legacyWellData{UUID: g, Replicated: map[guuid.UUID][]WellInfo{g: {{Name: "w"}}}}, &iwd)
	if iwd.UUID != u || len(iwd.Replicated[u]) != 1 {
		t.Fatalf("legacy -> new well data mismatch: %+v", iwd)
	}
	var lwd legacyWellData
	gobRoundTrip(t, IndexerWellData{UUID: u, Replicated: map[UUID][]WellInfo{u: {{Name: "w"}}}}, &lwd)
	if lwd.UUID != g || len(lwd.Replicated[g]) != 1 {
		t.Fatalf("new -> legacy well data mismatch: %+v", lwd)
	}
}

func TestUUIDJSONMatchesGoogleUUID(t *testing.T) {
	g := guuid.New()
	want, err := json.Marshal(legacyThing{UUID: g})
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(struct {
		UUID     UUID
		UID      int32
		Contents []byte
	}{UUID: UUID(g)})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %s, want %s", got, want)
	}
	var u UUID
	if err := json.Unmarshal([]byte(`"`+g.String()+`"`), &u); err != nil || u != UUID(g) {
		t.Fatalf("unmarshal: %v %v", u, err)
	}
	if u.String() != g.String() || uuid.UUID(u).String() != g.String() {
		t.Fatalf("String mismatch: %s != %s", u, g)
	}
}

func TestUUIDUnmarshalBinaryLength(t *testing.T) {
	var u UUID
	if err := u.UnmarshalBinary(make([]byte, 15)); err == nil {
		t.Fatal("expected error for short input")
	}
}
