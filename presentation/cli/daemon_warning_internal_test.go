package cli

import "testing"

func TestDaemonCommandsNeverRunCoreReclaimInspector(t *testing.T) {
	root := NewRootCLI().Command()
	for _, name := range []string{"daemon serve", "daemon status", "daemon shutdown preview", "daemon shutdown cancel", "daemon shutdown confirm", "daemon shutdown receipt", "daemon shutdown history", "daemon update status", "daemon update attempt", "daemon update handoff"} {
		command := lookupCommandPath(root, name)
		if command == nil || !shouldSkipCompactReclaimWarning(command) {
			t.Fatalf("daemon command can inspect core store: %s", name)
		}
	}
}
