package cli

import "testing"

func TestRuntimeHistoryNeverRunsDefaultCoreReclaimInspection(t *testing.T) {
	command := lookupCommandPath(NewRootCLI().Command(), "runtime history page")
	if command == nil || !shouldSkipCompactReclaimWarning(command) {
		t.Fatal("history command can inspect default core store")
	}
}
