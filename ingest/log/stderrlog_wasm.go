//go:build wasm || wasi
// +build wasm wasi

/*************************************************************************
 * Copyright 2024 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package log

import (
	"errors"
)

func newStderrLogger(fileOverride string, cb StderrCallback) (lgr *Logger, err error) {
	err = errors.New("stderr logger not avialable on wasm")
	return
}
