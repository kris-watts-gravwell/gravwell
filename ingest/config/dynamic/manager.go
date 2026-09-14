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
	"reflect"
)

type Manager interface {
	Close() error
	Signal() <-chan struct{} // read only struct
	Load(any) (err error)
}

type NopManager struct {
}

// Close always succeeds
func (n *NopManager) Close() error {
	return nil
}

// Signal returns a nil channel that will never fire
func (n *NopManager) Signal() (v <-chan struct{}) {
	return
}

func (n *NopManager) Load(v any) (err error) {
	if v == nil {
		return errors.New("nil object")
	} else if reflect.ValueOf(v).Kind() != reflect.Ptr {
		return errors.New("object must be a pointer")
	}
	// check that we can write to the pointer
	return nil // all good
}

type DynamicConfigManager struct {
	NopManager //TODO FIXME
	Config     // embed the config
	ch         <-chan struct{}
}

func NewDynamicConfigManager(c Config) (m Manager, err error) {
	if err = c.Validate(); err != nil {
		return
	}
	m = &DynamicConfigManager{
		Config: c,
		ch:     make(chan struct{}, 1),
	}
	return
}
