/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// testConfig is a Config that is valid apart from whatever the caller overrides.
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Webserver:  []string{`10.0.0.1:8080`},
		Auth_Token: `token`,
		Storage:    t.TempDir(),
	}
}

// TestConfigVerifyRequired covers the guards on the required members.  Class is
// optional, so a config that omits it must still validate.
func TestConfigVerifyRequired(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name string
		c    Config
	}{
		{`no webserver`, Config{Auth_Token: `t`, Storage: dir}},
		{`empty webserver list`, Config{Webserver: []string{}, Auth_Token: `t`, Storage: dir}},
		{`no auth token`, Config{Webserver: []string{`10.0.0.1`}, Storage: dir}},
		{`no storage`, Config{Webserver: []string{`10.0.0.1`}, Auth_Token: `t`}},
	} {
		if err := tc.c.Verify(); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
	c := Config{}
	if c.Enabled() {
		t.Fatal("failed to catch empty struct as not enabled")
	} else if err := c.Verify(); err != nil {
		t.Fatal("Verify on empty Config failed", err)
	}

	// a nil config is a programming error, not a panic
	var nilc *Config
	if err := nilc.Verify(); err == nil {
		t.Error(`a nil config should not validate`)
	}

	// Class is optional
	c = testConfig(t)
	if err := c.Verify(); err != nil {
		t.Errorf("a config with no Class should validate: %v", err)
	}
	c = testConfig(t)
	c.Class = `edge`
	if err := c.Verify(); err != nil {
		t.Errorf("a config with a Class should validate: %v", err)
	}
}

// TestConfigVerifyWebserverNormalize checks that endpoints are parsed as URLs and that
// an endpoint with no protocol gets http:// attached.
func TestConfigVerifyWebserverNormalize(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{`bare host and port`, `10.0.0.1:8080`, `http://10.0.0.1:8080`},
		{`bare host`, `localhost`, `http://localhost`},
		{`bare host with path`, `foo.example.com:443/api`, `http://foo.example.com:443/api`},
		{`bare IPv6`, `[::1]:8080`, `http://[::1]:8080`},
		{`http preserved`, `http://10.0.0.1:8080`, `http://10.0.0.1:8080`},
		{`https preserved`, `https://foo.example.com/api/v1`, `https://foo.example.com/api/v1`},
		{`scheme lowercased`, `HTTPS://foo.example.com`, `https://foo.example.com`},
		{`surrounding whitespace`, "  10.0.0.1:8080\t", `http://10.0.0.1:8080`},
		// a protocol relative endpoint already contains a :// in its path, so it never
		// picks up the prefix and has to have its scheme filled in after the parse
		{`protocol relative`, `//foo.example.com/a://b`, `http://foo.example.com/a://b`},
		{`protocol relative with port`, `//foo.example.com:8080/x://y`, `http://foo.example.com:8080/x://y`},
	} {
		c := testConfig(t)
		c.Webserver = []string{tc.in}
		if err := c.Verify(); err != nil {
			t.Errorf("%s: %q should validate: %v", tc.name, tc.in, err)
		} else if c.Webserver[0] != tc.want {
			t.Errorf("%s: %q normalized to %q, want %q", tc.name, tc.in, c.Webserver[0], tc.want)
		}
	}

	// every endpoint in the list is normalized, in place and in order
	c := testConfig(t)
	c.Webserver = []string{`10.0.0.1:8080`, `https://foo.example.com`, ` localhost `}
	want := []string{`http://10.0.0.1:8080`, `https://foo.example.com`, `http://localhost`}
	if err := c.Verify(); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(c.Webserver, want) {
		t.Errorf("normalized to %v, want %v", c.Webserver, want)
	}

	// normalization is stable, validating an already validated config changes nothing
	if err := c.Verify(); err != nil {
		t.Fatal(err)
	} else if !reflect.DeepEqual(c.Webserver, want) {
		t.Errorf("revalidation changed the endpoints to %v, want %v", c.Webserver, want)
	}
}

// TestConfigVerifyWebserverErrors covers endpoints that cannot be used.
func TestConfigVerifyWebserverErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		ws   []string
	}{
		{`empty endpoint`, []string{``}},
		{`whitespace endpoint`, []string{"  \t"}},
		{`empty among valid`, []string{`10.0.0.1:8080`, ``}},
		{`no host`, []string{`http://`}},
		{`no host with path`, []string{`http:///api`}},
		{`ftp scheme`, []string{`ftp://foo.example.com`}},
		{`file scheme`, []string{`file:///tmp/foo`}},
		{`ws scheme`, []string{`ws://foo.example.com`}},
		{`control character`, []string{"http://foo.example.com/\x7f"}},
		{`bad escape`, []string{`http://foo.example.com/%zz`}},
		{`valid then invalid`, []string{`https://foo.example.com`, `ftp://bar.example.com`}},
	} {
		c := testConfig(t)
		c.Webserver = tc.ws
		if err := c.Verify(); err == nil {
			t.Errorf("%s: %v should not validate", tc.name, tc.ws)
		}
	}
}

// TestConfigVerifyStorageCreate checks that a missing Storage directory is created,
// including any missing parents.
func TestConfigVerifyStorageCreate(t *testing.T) {
	c := testConfig(t)
	c.Storage = filepath.Join(c.Storage, `parent`, `storage`)
	if err := c.Verify(); err != nil {
		t.Fatalf("Storage should have been created: %v", err)
	}
	if fi, err := os.Stat(c.Storage); err != nil {
		t.Fatalf("Storage was not created: %v", err)
	} else if !fi.IsDir() {
		t.Fatal(`Storage is not a directory`)
	}

	// an existing directory is accepted as is
	if err := c.Verify(); err != nil {
		t.Errorf("an existing Storage directory should validate: %v", err)
	}
}

// TestConfigVerifyStorageErrors covers Storage paths we cannot use.
func TestConfigVerifyStorageErrors(t *testing.T) {
	dir := t.TempDir()
	fpath := filepath.Join(dir, `file`)
	if err := os.WriteFile(fpath, []byte(`data`), 0660); err != nil {
		t.Fatal(err)
	}

	// Storage is a regular file rather than a directory
	c := testConfig(t)
	c.Storage = fpath
	if err := c.Verify(); err == nil {
		t.Error(`a file should not be accepted as Storage`)
	}

	// a parent component of the path is a regular file, the stat fails with something
	// other than a not exist error and we must not try to create it
	c = testConfig(t)
	c.Storage = filepath.Join(fpath, `storage`)
	if err := c.Verify(); err == nil {
		t.Error(`a Storage path below a file should not validate`)
	}

	// the file we walked through must be left alone
	if b, err := os.ReadFile(fpath); err != nil {
		t.Fatal(err)
	} else if string(b) != `data` {
		t.Errorf(`the file at the Storage path was modified: %q`, string(b))
	}
}

// TestConfigVerifyStorageNotWritable checks that a directory we cannot create files in
// is rejected, permission bits alone are not enough to know that.
func TestConfigVerifyStorageNotWritable(t *testing.T) {
	if runtime.GOOS == `windows` {
		t.Skip(`mode bits do not gate writes on windows`)
	} else if os.Geteuid() == 0 {
		t.Skip(`root ignores the mode bits`)
	}
	dir := filepath.Join(t.TempDir(), `readonly`)
	if err := os.Mkdir(dir, 0500); err != nil {
		t.Fatal(err)
	}
	c := testConfig(t)
	c.Storage = dir
	if err := c.Verify(); err == nil {
		t.Error(`a read only Storage directory should not validate`)
	}

	// a missing Storage below a directory we cannot write to cannot be created
	c = testConfig(t)
	c.Storage = filepath.Join(dir, `storage`)
	if err := c.Verify(); err == nil {
		t.Error(`a Storage directory that cannot be created should not validate`)
	}
}

// TestConfigVerifyStorageClean makes sure the writability probe does not leave
// anything behind in the storage directory.
func TestConfigVerifyStorageClean(t *testing.T) {
	c := testConfig(t)
	for range 4 {
		if err := c.Verify(); err != nil {
			t.Fatal(err)
		}
	}
	if dents, err := os.ReadDir(c.Storage); err != nil {
		t.Fatal(err)
	} else if len(dents) != 0 {
		names := make([]string, 0, len(dents))
		for _, dent := range dents {
			names = append(names, dent.Name())
		}
		t.Errorf(`Storage should be empty, found %v`, names)
	}
}
