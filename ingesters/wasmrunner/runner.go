/*************************************************************************
 * copyright 2024 gravwell, inc. all rights reserved.
 * contact: <legal@gravwell.io>
 *
 * this software may be modified and distributed under the terms of the
 * bsd 2-clause license. see the license file for details.
 **************************************************************************/

package wasmrunner

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/tetratelabs/wazero"
)

const (
	//We have to read the entire binary into memory, 128MB is huge.
	//This is just a safety net to prevent dump mistakes
	MaxWasmBinSize = 128 * 1024 * 1024

	// ExternalExitCode is just some constant that tells us we were asked to close
	// wasm programs can use/detect this value to do other things
	ExternalExitCode uint32 = 0x7fffffff
)

var (
	errOversizedBin = errors.New("WASM binary exceeds maximum allowed")
)

type WasmIngester struct {
	sync.Mutex
	cfg     WasmIngesterConfig
	rt      wazero.Runtime
	mod     wazero.CompiledModule
	ctx     context.Context
	cf      context.CancelFunc
	started bool
	running bool
}

type WasmIngesterConfig struct {
	Name string
	Guid uuid.UUID

	MemoryLimit uint32
}

func New(bin []byte, cfg WasmIngesterConfig) (wi *WasmIngester, err error) {
	if err = cfg.validate(); err != nil {
		return
	}

	wi = &WasmIngester{
		cfg: cfg,
	}
	//create a new context
	wi.ctx, wi.cf = context.WithCancel(context.Background())

	//build a runtime
	wi.rt = wazero.NewRuntimeWithConfig(wi.ctx, runtimeConfig(cfg))
	if wi.mod, err = wi.rt.CompileModule(wi.ctx, bin); err != nil {
		wi = nil
		err = fmt.Errorf("Failed to compile module %s module %w", cfg.Name, err)
		return
	}

	return
}

func (wi *WasmIngester) Start() (err error) {
	wi.Lock()
	if wi.running {
		err = errors.New("already running")

	} else {
		//TODO FIXME - Start the runtime
		wi.running = true
	}
	wi.Unlock()
	return
}

func (wi *WasmIngester) Stop() (err error) {
	wi.Lock()
	if wi.running {
		wi.cf() // call the cancel func

		//TODO FIXME - wait for the runtime to stop
		wi.running = false
	}
	wi.Unlock()
	return
}

func (wi *WasmIngester) Running() (r bool) {
	wi.Lock()
	r = wi.running
	wi.Unlock()
	return
}

func (wi *WasmIngester) Close() (err error) {
	wi.Lock()
	if wi.cf != nil {
		wi.cf()
	}
	if wi.rt != nil {
		//TODO signal the internal program we wish to exit

		//TODO wait for the internal program to exit

		//close everything out with gusto
		err = wi.rt.CloseWithExitCode(wi.ctx, ExternalExitCode)
	}
	wi.Unlock()
	return
}
