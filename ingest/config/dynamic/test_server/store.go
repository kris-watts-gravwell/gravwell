/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"uuid"

	"github.com/gravwell/gravwell/v4/ingest/config/dynamic"

	_ "modernc.org/sqlite" // pure Go, so this builds without a C toolchain
)

// schema is applied on every open, so pointing the server at a new file just works.
//
// The definition column carries the whole RunnerDefinition as JSON and is the record of
// record.  The other columns are duplicated out of it so that the database can enforce
// what matters and the UI can list things without decoding every row: one registration
// per kind, and one runner per UUID with kind and name unique together, which is the same
// rule the ingester's own manager applies.
const schema = `
CREATE TABLE IF NOT EXISTS kinds (
	ingester   TEXT NOT NULL,
	kind       TEXT NOT NULL,
	singleton  INTEGER NOT NULL DEFAULT 0,
	definition TEXT NOT NULL,
	updated    INTEGER NOT NULL,
	PRIMARY KEY (ingester, kind)
);
CREATE TABLE IF NOT EXISTS runners (
	uuid       TEXT PRIMARY KEY,
	kind       TEXT NOT NULL,
	name       TEXT NOT NULL,
	definition TEXT NOT NULL,
	updated    INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS runners_kind_name ON runners(kind, name);
CREATE TABLE IF NOT EXISTS ingesters (
	uuid      TEXT PRIMARY KEY,
	class     TEXT NOT NULL DEFAULT '',
	last_seen INTEGER NOT NULL
);
`

var (
	ErrNotFound = errors.New("not found")
)

// Store is the SQLite backing for registrations and configured runners.
type Store struct {
	db *sql.DB
}

// OpenStore opens or creates the database at pth.
func OpenStore(pth string) (s *Store, err error) {
	if pth == `` {
		return nil, errors.New("empty storage path")
	}
	var db *sql.DB
	// busy_timeout keeps the UI and the RPC side from tripping over each other, they are
	// both writing to one file
	if db, err = sql.Open(`sqlite`, pth+`?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)`); err != nil {
		return nil, fmt.Errorf("failed to open %s %w", pth, err)
	}
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to open %s %w", pth, err)
	}
	if _, err = db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to apply schema %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// ReplaceKinds records everything one ingester says it can run.
//
// The whole set is replaced rather than merged.  An ingester sends its complete list on
// every connection, so replacing is what lets a kind that has been dropped from a build
// actually disappear instead of lingering forever.
//
// Registrations are keyed by the ingester's UUID.  Two ingesters may advertise the same
// kind backed by different builds, and a webserver has to be able to tell them apart to
// know who is able to run what.
func (s *Store) ReplaceKinds(ingester uuid.UUID, class string, kinds []dynamic.RunnerDefinition) (err error) {
	if ingester == uuid.Nil() {
		return errors.New("registration has no ingester UUID")
	}
	var tx *sql.Tx
	if tx, err = s.db.Begin(); err != nil {
		return fmt.Errorf("failed to start a transaction %w", err)
	}
	defer tx.Rollback() // a no-op once committed, and the undo if anything below fails

	if _, err = tx.Exec(`DELETE FROM kinds WHERE ingester = ?`, ingester.String()); err != nil {
		return fmt.Errorf("failed to clear registrations %w", err)
	}
	now := time.Now().UnixNano()
	// remember the ingester itself, which is what lets the interface offer a real list of
	// UUIDs and classes to assign to rather than asking an operator to type them
	if _, err = tx.Exec(`INSERT INTO ingesters (uuid, class, last_seen) VALUES (?,?,?)
		ON CONFLICT(uuid) DO UPDATE SET class=excluded.class, last_seen=excluded.last_seen`,
		ingester.String(), class, now); err != nil {
		return fmt.Errorf("failed to record the ingester %w", err)
	}
	for _, rd := range kinds {
		if rd.Kind == `` {
			return errors.New("registration has no kind")
		}
		// a registration describes a type, it carries no identity, strip anything that
		// wandered in so the stored prototype is clean
		rd.Name = ``
		rd.UUID = uuid.Nil()
		var blob []byte
		if blob, err = json.Marshal(rd); err != nil {
			return fmt.Errorf("failed to encode definition %w", err)
		}
		if _, err = tx.Exec(`INSERT INTO kinds (ingester, kind, singleton, definition, updated)
			VALUES (?,?,?,?,?)`,
			ingester.String(), rd.Kind, rd.Singleton, string(blob), now); err != nil {
			return fmt.Errorf("failed to store kind %s %w", rd.Kind, err)
		}
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit registrations %w", err)
	}
	return
}

// IngesterKinds lists what one ingester says it can run.
func (s *Store) IngesterKinds(ingester uuid.UUID) (r []dynamic.RunnerDefinition, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT definition FROM kinds WHERE ingester = ? ORDER BY kind ASC`,
		ingester.String()); err != nil {
		return nil, fmt.Errorf("failed to list kinds %w", err)
	}
	defer rows.Close()
	return scanDefinitions(rows)
}

// Ingester is what the interface needs to offer an assignment target.
type Ingester struct {
	UUID     uuid.UUID
	Class    string
	Kinds    []string
	LastSeen time.Time
}

// Ingesters lists every ingester that has ever registered, newest first, with the kinds
// each one advertised.
func (s *Store) Ingesters() (r []Ingester, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT uuid, class, last_seen FROM ingesters
		ORDER BY last_seen DESC`); err != nil {
		return nil, fmt.Errorf("failed to list ingesters %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, class string
		var seen int64
		if err = rows.Scan(&raw, &class, &seen); err != nil {
			return
		}
		id, perr := uuid.Parse(raw)
		if perr != nil {
			continue // a row we cannot make sense of is not worth failing the page over
		}
		r = append(r, Ingester{UUID: id, Class: class, LastSeen: time.Unix(0, seen)})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	// fill in the kinds, one small query rather than a join so the decode stays simple
	for i := range r {
		var kinds []string
		if kinds, err = s.ingesterKindNames(r[i].UUID); err != nil {
			return nil, err
		}
		r[i].Kinds = kinds
	}
	return
}

// KindIngesters lists the ingesters that have registered a given kind, which is the set
// an operator may pin a configuration of that kind to.  Pinning to an ingester that
// cannot run the kind would produce a configuration that is never delivered.
func (s *Store) KindIngesters(kind string) (r []Ingester, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT i.uuid, i.class, i.last_seen
		FROM ingesters i JOIN kinds k ON k.ingester = i.uuid
		WHERE k.kind = ? ORDER BY i.last_seen DESC`, kind); err != nil {
		return nil, fmt.Errorf("failed to list ingesters for %s %w", kind, err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw, class string
		var seen int64
		if err = rows.Scan(&raw, &class, &seen); err != nil {
			return
		}
		if id, perr := uuid.Parse(raw); perr == nil {
			r = append(r, Ingester{UUID: id, Class: class, LastSeen: time.Unix(0, seen)})
		}
	}
	err = rows.Err()
	return
}

// Classes lists the distinct classes that have been seen, so the interface can offer them
// rather than asking an operator to remember what they called things.
func (s *Store) Classes() (r []string, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT DISTINCT class FROM ingesters
		WHERE class != '' ORDER BY class ASC`); err != nil {
		return nil, fmt.Errorf("failed to list classes %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var c string
		if err = rows.Scan(&c); err != nil {
			return
		}
		r = append(r, c)
	}
	err = rows.Err()
	return
}

// ingesterKindNames lists the kind names one ingester advertised.
func (s *Store) ingesterKindNames(id uuid.UUID) (r []string, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT kind FROM kinds WHERE ingester = ? ORDER BY kind ASC`,
		id.String()); err != nil {
		return nil, fmt.Errorf("failed to list kinds %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err = rows.Scan(&k); err != nil {
			return
		}
		r = append(r, k)
	}
	err = rows.Err()
	return
}

// Kinds lists the distinct kinds across every ingester, which is what the interface
// offers to configure.  Two ingesters advertising the same kind collapse to one entry,
// the most recently registered wins, because the operator is choosing a kind to
// configure rather than choosing an ingester.
func (s *Store) Kinds() (r []dynamic.RunnerDefinition, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT definition FROM kinds
		WHERE (kind, updated) IN (SELECT kind, MAX(updated) FROM kinds GROUP BY kind)
		GROUP BY kind ORDER BY kind ASC`); err != nil {
		return nil, fmt.Errorf("failed to list kinds %w", err)
	}
	defer rows.Close()
	if r, err = scanDefinitions(rows); err != nil {
		return nil, fmt.Errorf("failed to list kinds %w", err)
	}
	return
}

// Kind fetches one registration.
func (s *Store) Kind(kind string) (rd dynamic.RunnerDefinition, err error) {
	var blob string
	err = s.db.QueryRow(`SELECT definition FROM kinds WHERE kind = ? ORDER BY updated DESC LIMIT 1`, kind).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return rd, fmt.Errorf("kind %s %w", kind, ErrNotFound)
	} else if err != nil {
		return rd, fmt.Errorf("failed to read kind %s %w", kind, err)
	}
	if err = json.Unmarshal([]byte(blob), &rd); err != nil {
		return rd, fmt.Errorf("failed to decode kind %s %w", kind, err)
	}
	return
}

// PutRunner records a configured runner.  The UUID is the identity, so saving an edit
// updates in place while a new UUID is a new runner.
func (s *Store) PutRunner(rd dynamic.RunnerDefinition) (err error) {
	if rd.Kind == `` {
		return errors.New("runner has no kind")
	} else if rd.Name == `` {
		return errors.New("runner has no name")
	} else if rd.UUID == uuid.Nil() {
		return errors.New("runner has no UUID")
	}
	var blob []byte
	if blob, err = json.Marshal(rd); err != nil {
		return fmt.Errorf("failed to encode runner %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO runners (uuid, kind, name, definition, updated)
		VALUES (?,?,?,?,?)
		ON CONFLICT(uuid) DO UPDATE SET kind=excluded.kind, name=excluded.name,
			definition=excluded.definition, updated=excluded.updated`,
		rd.UUID.String(), rd.Kind, rd.Name, string(blob), time.Now().UnixNano())
	if err != nil {
		// the kind+name index is what catches two runners of one kind sharing a name
		return fmt.Errorf("failed to store runner %s/%s %w", rd.Kind, rd.Name, err)
	}
	return
}

// Runners lists every configured runner, grouped by kind then name so the UI order is
// stable across reloads.
func (s *Store) Runners() (r []dynamic.RunnerDefinition, err error) {
	var rows *sql.Rows
	if rows, err = s.db.Query(`SELECT definition FROM runners ORDER BY kind ASC, name ASC`); err != nil {
		return nil, fmt.Errorf("failed to list runners %w", err)
	}
	defer rows.Close()
	if r, err = scanDefinitions(rows); err != nil {
		return nil, fmt.Errorf("failed to list runners %w", err)
	}
	return
}

// Runner fetches one configured runner by UUID.
func (s *Store) Runner(id uuid.UUID) (rd dynamic.RunnerDefinition, err error) {
	var blob string
	err = s.db.QueryRow(`SELECT definition FROM runners WHERE uuid = ?`, id.String()).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return rd, fmt.Errorf("runner %v %w", id, ErrNotFound)
	} else if err != nil {
		return rd, fmt.Errorf("failed to read runner %v %w", id, err)
	}
	if err = json.Unmarshal([]byte(blob), &rd); err != nil {
		return rd, fmt.Errorf("failed to decode runner %v %w", id, err)
	}
	return
}

// DeleteRunner removes a configured runner.
func (s *Store) DeleteRunner(id uuid.UUID) (err error) {
	var res sql.Result
	if res, err = s.db.Exec(`DELETE FROM runners WHERE uuid = ?`, id.String()); err != nil {
		return fmt.Errorf("failed to delete runner %v %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("runner %v %w", id, ErrNotFound)
	}
	return
}

// scanDefinitions decodes a query of definition blobs.
func scanDefinitions(rows *sql.Rows) (r []dynamic.RunnerDefinition, err error) {
	for rows.Next() {
		var blob string
		if err = rows.Scan(&blob); err != nil {
			return
		}
		var rd dynamic.RunnerDefinition
		if err = json.Unmarshal([]byte(blob), &rd); err != nil {
			return nil, fmt.Errorf("failed to decode a stored definition %w", err)
		}
		r = append(r, rd)
	}
	err = rows.Err()
	return
}
