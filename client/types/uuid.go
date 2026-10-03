/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package types

import (
	"fmt"
	"uuid"
)

// UUID is a standard library uuid.UUID that also implements encoding.BinaryMarshaler
// and encoding.BinaryUnmarshaler. Gob prefers the binary marshalers over the text
// marshalers, so this keeps the gob wire format identical to github.com/google/uuid,
// which these types used previously. JSON and text encodings are unchanged.
type UUID uuid.UUID

// String returns the lowercase hex-and-dash representation of u.
func (u UUID) String() string {
	return uuid.UUID(u).String()
}

// MarshalText implements encoding.TextMarshaler.
func (u UUID) MarshalText() ([]byte, error) {
	return uuid.UUID(u).MarshalText()
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (u *UUID) UnmarshalText(b []byte) error {
	return (*uuid.UUID)(u).UnmarshalText(b)
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (u UUID) MarshalBinary() ([]byte, error) {
	return u[:], nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (u *UUID) UnmarshalBinary(b []byte) error {
	if len(b) != 16 {
		return fmt.Errorf("invalid UUID (got %d bytes)", len(b))
	}
	copy(u[:], b)
	return nil
}
