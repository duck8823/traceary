package cli

import "testing"

func TestStorePayloadRehearsalCommandRemoved(t *testing.T) {
	t.Parallel()
	store := findCommandOrNil(NewRootCLI().Command(), "store")
	if store == nil {
		t.Fatal("store command is not registered")
	}
	if got := findCommandOrNil(store, "payload-rehearsal"); got != nil {
		t.Fatal("store payload-rehearsal must be removed by #1872")
	}
}
