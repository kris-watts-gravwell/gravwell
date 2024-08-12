//go:build wasi || wasm
// +build wasi wasm

package chancacher

import "errors"

var (
	errNotInWasm = errors.New("Unsupported in wasm mode")
)

type locker struct {
}

func newLocker(pth string) *locker {
	return &locker{}
}

func (l *locker) TryLock() (bool, error) {
	return false, errNotInWasm
}

func (l *locker) Unlock() error {
	return errNotInWasm
}
