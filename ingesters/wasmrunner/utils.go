/*************************************************************************
 * copyright 2024 gravwell, inc. all rights reserved.
 * contact: <legal@gravwell.io>
 *
 * this software may be modified and distributed under the terms of the
 * bsd 2-clause license. see the license file for details.
 **************************************************************************/

package wasmrunner

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

// NewReader is a convienence wrapper around New that accepts an io.Reader instead of a byte slice.
func NewReader(rdr io.Reader, cfg WasmIngesterConfig) (wi *WasmIngester, err error) {
	var bts []byte

	// add one extra byte so we can detect oversized reads
	lr := io.LimitReader(rdr, MaxWasmBinSize+1)
	if bts, err = io.ReadAll(lr); err != nil {
		return
	} else if len(bts) > MaxWasmBinSize {
		err = errOversizedBin
		return
	}
	wi, err = New(bts, cfg)
	return
}

func NewFile(pth string, cfg WasmIngesterConfig) (wi *WasmIngester, err error) {
	var fin *os.File
	if fin, err = os.Open(pth); err == nil {
		if wi, err = NewReader(fin, cfg); err != nil {
			fin.Close()
		} else {
			err = fin.Close()
		}
	}
	return
}

func runtimeConfig(wic WasmIngesterConfig) wazero.RuntimeConfig {
	var feat api.CoreFeatures

	//get the runtime config all spun up
	cfg := wazero.NewRuntimeConfig()

	//enable bulk memory operations for tinygo v0.24+
	feat.SetEnabled(api.CoreFeatureBulkMemoryOperations, true)

	//enable CoreFeaturesV2
	feat.SetEnabled(api.CoreFeaturesV2, true)

	cfg = cfg.WithCoreFeatures(feat) //add our features

	//check if the config has memory limits
	if pl := wic.memPageLimit(); pl > 0 {
		cfg = cfg.WithMemoryLimitPages(pl)
	}

	//always close on context done
	return cfg.WithCloseOnContextDone(true)
}

// validate will check the config for valid items and also
// update any unitialized values (like name and GUID)
func (cfg *WasmIngesterConfig) validate() (err error) {
	// a zero UUID means just make one
	if cfg.Guid == uuid.Nil {
		cfg.Guid = uuid.New()
	}

	//an empty name gets a random name
	if cfg.Name == `` {
		cfg.Name = fmt.Sprintf("wasm-ingester-%d", time.Now().Unix())
	}
	return
}

func (cfg WasmIngesterConfig) memPageLimit() uint32 {
	//TODO FIXME
	return 0
}
