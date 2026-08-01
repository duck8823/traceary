package cli

import "testing"

func TestPayloadRehearsalExposesNoActivationCommand(t *testing.T) {
	group := NewRootCLI().newStorePayloadRehearsalCommand()
	want := map[string]bool{"preview": true, "run": true, "resume": true, "scrub": true, "rollback": true}
	for _, command := range group.Commands() {
		if command.Name() == "activate" {
			t.Fatal("v0.34 must not expose activation")
		}
		delete(want, command.Name())
		if command.Name() != "preview" && command.Name() != "scrub" && command.Flags().Lookup("backup") == nil {
			t.Fatalf("%s lacks rollback artifact flag", command.Name())
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing rehearsal commands: %v", want)
	}
}
