package proxy

import (
	"testing"

	"zongheng-vpn/shared/paths"
)

func TestPrivateStateRejectsOtherSecretsAndUnboundedWrites(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	for _, name := range []string{"engine-state.json", "../device-v2-state.json", "run/engine-state.json", ""} {
		if _, err := NewPrivateState(home, name); err == nil {
			t.Fatalf("accepted other secret or path %q", name)
		}
	}
	s, err := NewPrivateState(home, "device-v2-state.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, make([]byte, (64<<10)+1)} {
		if err := s.Write(data); err == nil {
			t.Fatal("unbounded state accepted")
		}
	}
}
