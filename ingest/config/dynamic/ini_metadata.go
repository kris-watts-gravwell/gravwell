/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// A Variable's Metadata is a note about a value rather than part of it: which stored
// secret a member was filled from, where a default came from, anything a consumer needs
// alongside the value that the value itself has nowhere to put.  A config file has
// nowhere to put it either, so it rides in a comment above the member it describes:
//
//	[mimecast "prod"]
//		#gravwell-metadata "Client-Secret" "secret-key"="prod/mimecast"
//		Client-Secret="shh"
//
// A comment is the only place it can go that the gcfg parser, and every operator reading
// the file, is entitled to ignore completely: a config written with metadata loads into a
// plugin exactly as one written without it does.  It survives the trip back out because
// ParseINIMetadata reads the comments the parser skipped.
//
// Names, keys and values are all written as Go quoted strings.  Quoting is not decoration
// here, it is what makes the round trip total: a key or value may hold a quote, a
// backslash, a newline, a comment character or a control character, and metaQuote escapes
// every one of them into something printable, which strconv.Unquote turns back into the
// exact original bytes.
const (
	// iniMetadataComment opens a metadata comment and tells one apart from the ordinary
	// comments a config file is free to carry.  gcfg accepts a hash or a semicolon, and
	// every config this product ships uses a hash.
	iniMetadataComment = `#` + iniMetadataMarker

	// iniMetadataMarker is the word that marks the comment, without its opening
	// character.  A comment is read back on either character, since only the word makes
	// it ours.
	iniMetadataMarker = `gravwell-metadata`
)

// ErrBadMetadataComment is returned when a line opens with the metadata marker but does
// not carry what a metadata comment carries.  It is deliberately not silent: a comment
// that was meant to restore something and cannot is a lost value, not a stray comment.
var ErrBadMetadataComment = errors.New("malformed metadata comment")

// metadataComment renders a variable's Metadata as a single INI comment line, without a
// leading prefix or a trailing newline.  A variable with no metadata renders as nothing.
//
// Keys are emitted in sorted order so that a definition renders to the same bytes every
// time.  Go's map iteration is random, and the client compares a rendered block against
// what is already on disk to decide whether to rewrite it: an unsorted rendering would
// churn the file, and the runner with it, on every poll.
func (v Variable) metadataComment() string {
	if len(v.Metadata) == 0 {
		return ``
	}
	var sb strings.Builder
	sb.WriteString(iniMetadataComment)
	sb.WriteByte(' ')
	sb.WriteString(metaQuote(v.Name))
	for _, k := range slices.Sorted(maps.Keys(v.Metadata)) {
		sb.WriteByte(' ')
		sb.WriteString(metaQuote(k))
		sb.WriteByte('=')
		sb.WriteString(metaQuote(v.Metadata[k]))
	}
	return sb.String()
}

// emitIniMetadata writes a variable's metadata comment, if it has any.
//
// Metadata is written whether or not the variable has a value.  The two are independent:
// metadata describes the member, and a member that is currently unset can still have been
// pointed at a stored secret.  Dropping it with the value would mean a configuration lost
// part of itself by being saved.
func (v Variable) emitIniMetadata(w io.Writer, prefix string) (err error) {
	if c := v.metadataComment(); c != `` {
		_, err = fmt.Fprintf(w, "%s%s\n", prefix, c)
	}
	return
}

// ParseINIMetadata pulls the variable metadata back out of an INI blob, keyed by variable
// name and then by metadata key.  A blob carrying no metadata comments comes back as a
// nil map and no error, which is the ordinary case for a config written by anything else.
//
// This reads the blob a line at a time, which is sound because nothing INI writes spans
// one: a value holding a newline takes the quoted path, where the newline is escaped.  A
// blob from somewhere else could in principle carry a raw string that spans lines and so
// a value that looks like a metadata comment, and the only thing that costs is metadata
// that was not really there, on a file this package did not write.
//
// Metadata under more than one section is refused.  The comments name a variable, not a
// section, so a file holding two runners has two answers to the same question and no way
// to tell them apart.  INI renders exactly one section, so the blob this is meant for
// never has that problem, and a blob that does is better refused than merged.
func ParseINIMetadata(ini string) (r map[string]map[string]string, err error) {
	var section, found string
	for line := range strings.SplitSeq(ini, "\n") {
		if line = strings.TrimSpace(line); line == `` {
			continue
		} else if line[0] == '[' {
			section = line
			continue
		}
		body, ok := metadataCommentBody(line)
		if !ok {
			continue
		}
		var name string
		var kv map[string]string
		if name, kv, err = parseMetadataComment(body); err != nil {
			return nil, err
		}
		if r == nil {
			r, found = map[string]map[string]string{}, section
		} else if section != found {
			return nil, fmt.Errorf("%w: metadata under both %s and %s, the comments cannot say which runner %s belongs to",
				ErrBadMetadataComment, sectionName(found), sectionName(section), name)
		}
		if _, dup := r[name]; dup {
			return nil, fmt.Errorf("%w: %s has two metadata comments", ErrBadMetadataComment, name)
		}
		r[name] = kv
	}
	return
}

// RestoreMetadata puts the metadata written into an INI blob back onto the variables it
// describes, which is the other half of the round trip: a definition rendered to a config
// file and mapped back out of one comes back with its values and none of its metadata,
// because the values are all a plugin config has room for.
//
// Metadata naming a variable this definition does not have is dropped rather than
// refused.  A plugin is free to rename or retire a member between versions, and a note
// about a member that no longer exists is not a reason to refuse to load a config that is
// otherwise fine.
func (c *RunnerDefinition) RestoreMetadata(ini string) (err error) {
	var md map[string]map[string]string
	if md, err = ParseINIMetadata(ini); err != nil || len(md) == 0 {
		return
	}
	for i, v := range c.Variables {
		if kv, ok := md[v.Name]; ok {
			c.Variables[i].Metadata = kv
		}
	}
	return
}

// metadataCommentBody reports whether a line is a metadata comment and hands back what
// follows the marker.  The line is expected to have been trimmed already.
//
// The marker has to be a whole word: a comment opening with gravwell-metadata-format is
// somebody writing about this, not an instance of it, and reading it as one would turn a
// note into an error.
func metadataCommentBody(line string) (body string, ok bool) {
	if len(line) == 0 || (line[0] != ';' && line[0] != '#') {
		return
	}
	rest, cut := strings.CutPrefix(strings.TrimSpace(line[1:]), iniMetadataMarker)
	if !cut {
		return
	} else if rest == `` {
		// ours, and empty: a marker with nothing after it is a truncated metadata
		// comment rather than a comment about something else
		return ``, true
	} else if rest[0] != ' ' && rest[0] != '\t' {
		return ``, false
	}
	return strings.TrimSpace(rest), true
}

// parseMetadataComment reads the body of a metadata comment: a quoted variable name
// followed by one or more quoted key="value" pairs.
func parseMetadataComment(body string) (name string, kv map[string]string, err error) {
	var rest string
	if name, rest, err = scanQuoted(body); err != nil {
		err = fmt.Errorf("%w: variable name: %w", ErrBadMetadataComment, err)
		return
	}
	kv = map[string]string{}
	for {
		if rest = strings.TrimLeft(rest, " \t"); rest == `` {
			break
		}
		var k, v string
		if k, rest, err = scanQuoted(rest); err != nil {
			err = fmt.Errorf("%w: %s: key: %w", ErrBadMetadataComment, name, err)
			return
		}
		var ok bool
		if rest, ok = strings.CutPrefix(rest, `=`); !ok {
			err = fmt.Errorf("%w: %s: key %q is not followed by =", ErrBadMetadataComment, name, k)
			return
		} else if v, rest, err = scanQuoted(rest); err != nil {
			err = fmt.Errorf("%w: %s: value for %q: %w", ErrBadMetadataComment, name, k, err)
			return
		} else if _, dup := kv[k]; dup {
			err = fmt.Errorf("%w: %s: %q appears twice", ErrBadMetadataComment, name, k)
			return
		}
		kv[k] = v
	}
	// a comment exists to carry pairs, one carrying none was truncated or mangled on its
	// way here, and handing back an empty map would quietly turn that into "no metadata"
	if len(kv) == 0 {
		err = fmt.Errorf("%w: %s has no key/value pairs", ErrBadMetadataComment, name)
		kv = nil
	}
	return
}

// metaQuote renders s as a quoted string holding nothing but printable characters.
//
// A comment is a line, so nothing that ends one may appear in it, and a newline is only
// the most obvious of those: a config file is read by editors, terminals and log viewers
// as well as by this package, and a carriage return, a form feed or a stray escape byte
// can all make a line render as something other than what it says.  So every control
// character goes out as a numeric escape rather than a literal, \x for a byte and \u or
// \U for a rune, all of which strconv.Unquote reads straight back.
//
// Bytes that are not valid UTF-8 are escaped for a related reason: written literally they
// come back from Unquote as replacement characters, which is a silently different value.
// Printable text, including the whole of printable Unicode, is written as itself, because
// a comment nobody can read is a comment nobody will maintain.
func metaQuote(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 2)
	sb.WriteByte('"')
	for i := 0; i < len(s); {
		// the ASCII range covers everything with a special meaning here, and handling it
		// a byte at a time keeps the decoder out of the way of the rest
		if c := s[i]; c < utf8.RuneSelf {
			i++
			switch {
			case c == '\\' || c == '"':
				sb.WriteByte('\\')
				sb.WriteByte(c)
			case c < 0x20 || c == 0x7f:
				fmt.Fprintf(&sb, `\x%02x`, c)
			default:
				sb.WriteByte(c)
			}
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			// a byte belonging to no valid sequence, escaped as the byte it is
			fmt.Fprintf(&sb, `\x%02x`, s[i])
		case !strconv.IsPrint(r):
			// valid and unprintable: a line separator or a bidi override is not a
			// newline, but it is no more readable on a line than one
			if r > 0xffff {
				fmt.Fprintf(&sb, `\U%08x`, r)
			} else {
				fmt.Fprintf(&sb, `\u%04x`, r)
			}
		default:
			sb.WriteString(s[i : i+size])
		}
		i += size
	}
	sb.WriteByte('"')
	return sb.String()
}

// scanQuoted pulls one Go quoted string off the front of s and hands back its value along
// with whatever follows it.
//
// Finding the end by scanning to the first unescaped quote is exact for anything
// metaQuote produced, which never leaves a bare double quote inside a string, and
// strconv.Unquote rejects anything else this hands it.
func scanQuoted(s string) (val, rest string, err error) {
	if len(s) == 0 || s[0] != '"' {
		err = fmt.Errorf("expected a quoted string, got %q", s)
		return
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++ // whatever follows a backslash belongs to the escape, it cannot terminate
		case '"':
			if val, err = strconv.Unquote(s[:i+1]); err != nil {
				err = fmt.Errorf("%q is not a valid quoted string: %w", s[:i+1], err)
				return
			}
			rest = s[i+1:]
			return
		}
	}
	err = fmt.Errorf("unterminated quoted string %q", s)
	return
}

// sectionName describes a section for an error message.  Metadata ahead of any section
// header at all is possible in a hand written file, and "the section before any section"
// needs a name too.
func sectionName(s string) string {
	if s == `` {
		return `no section`
	}
	return s
}
