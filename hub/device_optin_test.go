package main

import "testing"

func TestDeviceAuthorityDefaultOffAndSeparateInterface(t *testing.T) {
	t.Setenv("ZHHUB_DEVICE_AUTH_ENABLED", "")
	if _, enabled, err := deviceConfigFromEnv(); err != nil || enabled {
		t.Fatal("default started v2 authority")
	}
	t.Setenv("ZHHUB_DEVICE_AUTH_ENABLED", "true")
	if _, _, err := deviceConfigFromEnv(); err == nil {
		t.Fatal("invalid opt-in accepted")
	}
	t.Setenv("ZHHUB_DEVICE_AUTH_ENABLED", "1")
	t.Setenv("ZHHUB_DEVICE_WG_INTERFACE", "wg0")
	if _, _, err := deviceConfigFromEnv(); err == nil {
		t.Fatal("legacy concurrent writer accepted")
	}
	t.Setenv("ZHHUB_WG_INTERFACE", " wg0 ")
	if _, _, err := deviceConfigFromEnv(); err == nil {
		t.Fatal("trimmed legacy interface bypassed isolation")
	}
	t.Setenv("ZHHUB_DEVICE_WG_INTERFACE", "wg-device0")
	if _, enabled, err := deviceConfigFromEnv(); err != nil || !enabled {
		t.Fatal("explicit isolated interface refused")
	}
}
