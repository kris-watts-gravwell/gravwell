//go:build !wasi && !wasm
// +build !wasi,!wasm

package chancacher

import (
	"github.com/gofrs/flock"
)

type locker struct {
	flock.Flock
}

func newLocker(pth string) *locker {
	return &locker{
		Flock: flock.New(pth),
	}
}
