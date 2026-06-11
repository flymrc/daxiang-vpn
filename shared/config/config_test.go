package config

import "testing"

func TestCustomerNameUsesDisplayName(t *testing.T) {
	cfg := EgressConfig{
		Name:        "jp-android-01",
		DisplayName: "au",
	}

	if got, want := cfg.CustomerName(), "au"; got != want {
		t.Fatalf("CustomerName() = %q, want %q", got, want)
	}
}

func TestCustomerNameKeepsCustomDisplayName(t *testing.T) {
	cfg := EgressConfig{
		Name:        "jp-android-01",
		DisplayName: "au",
	}

	if got, want := cfg.CustomerName(), "au"; got != want {
		t.Fatalf("CustomerName() = %q, want %q", got, want)
	}
}
