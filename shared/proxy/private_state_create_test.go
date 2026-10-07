package proxy

import (
	"bytes"
	"errors"
	"os"
	"sync"
	"testing"

	"zongheng-vpn/shared/paths"
)

func TestPrivateStateCreateCannotReplaceAnExistingRegistration(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	prepareTestHome(t, home)
	store, err := NewPrivateState(home, "device-v2-state.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create([]byte("first-owner")); err != nil {
		t.Fatal(err)
	}
	if err := store.Create([]byte("replacement")); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive publication did not reject existing target: %v", err)
	}
	data, err := store.Read()
	if err != nil || string(data) != "first-owner" {
		t.Fatalf("registration replaced or lost protection: %v", err)
	}
}

func TestPrivateStateConcurrentCreationPublishesOneCompleteFile(t *testing.T) {
	home := paths.FromRoot(t.TempDir())
	prepareTestHome(t, home)
	store, err := NewPrivateState(home, "device-v2-state.json")
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 8)
	values := make(chan []byte, 8)
	start := make(chan struct{})
	for i := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			candidate := bytes.Repeat([]byte{byte('a' + i)}, 32<<10)
			<-start
			err := store.Create(candidate)
			if err == nil {
				values <- candidate
			}
			results <- err
		}()
	}
	close(start)
	group.Wait()
	close(results)
	winners := 0
	for err := range results {
		if err == nil {
			winners++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("exclusive creators=%d", winners)
	}
	data, err := store.Read()
	if err != nil || !bytes.Equal(data, <-values) {
		t.Fatalf("published partial or unprotected content: %v", err)
	}
}
