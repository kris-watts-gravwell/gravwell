/*************************************************************************
 * Copyright 2026 Gravwell, Inc. All rights reserved.
 * Contact: <legal@gravwell.io>
 *
 * This software may be modified and distributed under the terms of the
 * BSD 2-clause license. See the LICENSE file for details.
 **************************************************************************/

package dynamic

import "testing"

// TestAuthTokenFromEnv covers supplying the shared control secret through the environment,
// which is how a container hands the same value to the webserver and to the ingester
// without baking a credential into an image.
func TestAuthTokenFromEnv(t *testing.T) {
	const fromEnv = `secret-from-the-environment`
	const fromFile = `secret-from-the-config-file`

	newCfg := func(token string, dir string) *Config {
		return &Config{
			Webserver:  []string{`127.0.0.1:80`},
			Auth_Token: token,
			Storage:    dir,
		}
	}

	t.Run(`fills in an absent token`, func(t *testing.T) {
		t.Setenv(envIngestControlAuth, fromEnv)
		c := newCfg(``, t.TempDir())
		if err := c.Verify(); err != nil {
			t.Fatalf("a config whose token comes from the environment was refused: %v", err)
		}
		if c.Auth_Token != fromEnv {
			t.Fatalf("token is %q, expected the environment's %q", c.Auth_Token, fromEnv)
		}
	})

	t.Run(`the config file wins`, func(t *testing.T) {
		// same precedence as every other secret here: the environment fills in a value
		// that is absent, it does not override one an operator wrote down
		t.Setenv(envIngestControlAuth, fromEnv)
		c := newCfg(fromFile, t.TempDir())
		if err := c.Verify(); err != nil {
			t.Fatal(err)
		}
		if c.Auth_Token != fromFile {
			t.Fatalf("token is %q, the environment overrode the config file", c.Auth_Token)
		}
	})

	t.Run(`still required when neither supplies it`, func(t *testing.T) {
		t.Setenv(envIngestControlAuth, ``)
		c := newCfg(``, t.TempDir())
		if err := c.Verify(); err == nil {
			t.Fatal("a config with no token from either source was accepted")
		}
	})

	t.Run(`the environment alone does not enable dynamic config`, func(t *testing.T) {
		// an ingester whose configuration never asked for dynamic config must not be
		// switched into it by an environment variable that happens to be set
		t.Setenv(envIngestControlAuth, fromEnv)
		var c Config
		if c.Enabled() {
			t.Fatal("an empty config reports itself enabled")
		}
		if err := c.Verify(); err != nil {
			t.Fatalf("verifying a disabled config failed: %v", err)
		}
		if c.Auth_Token != `` {
			t.Fatalf("a disabled config picked up a token %q from the environment", c.Auth_Token)
		}
	})
}
