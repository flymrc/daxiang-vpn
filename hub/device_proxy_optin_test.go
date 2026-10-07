package main

import "testing"

func TestProxyBootstrapRequiresExplicitProfileOptIn(t *testing.T) {
	t.Setenv("ZHHUB_DEVICE_AUTH_ENABLED", "0")
	t.Setenv("ZHHUB_DEVICE_PROXY_PROFILE", "SYNTHETIC_PROFILE_PATH")
	if c, on, err := deviceConfigFromEnv(); err != nil || on || c.ProxyProfilePath != "" {
		t.Fatal("profile alone enabled authority")
	}
	t.Setenv("ZHHUB_DEVICE_AUTH_ENABLED", "1")
	t.Setenv("ZHHUB_WG_INTERFACE", "wg0")
	t.Setenv("ZHHUB_DEVICE_WG_INTERFACE", "wg-customer")
	t.Setenv("ZHHUB_DEVICE_PROXY_PROFILE", "")
	c, on, err := deviceConfigFromEnv()
	if err != nil || !on || c.ProxyProfilePath != "" {
		t.Fatal("profile has an implicit default")
	}
	t.Setenv("ZHHUB_DEVICE_PROXY_PROFILE", "SYNTHETIC_PROFILE_PATH")
	c, on, err = deviceConfigFromEnv()
	if err != nil || !on || c.ProxyProfilePath != "SYNTHETIC_PROFILE_PATH" {
		t.Fatal("explicit profile lost")
	}
	t.Setenv("ZHHUB_DEVICE_WG_INTERFACE", "wg0")
	if _, _, err := deviceConfigFromEnv(); err == nil {
		t.Fatal("profile bypassed interface isolation")
	}
}
