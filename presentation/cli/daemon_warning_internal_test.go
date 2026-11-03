package cli

import "testing"

func TestDaemonCommandsNeverRunCoreReclaimInspector(t *testing.T) {
	root := NewRootCLI().Command()
	for _, name := range []string{"daemon serve", "daemon status"} {
		command := lookupCommandPath(root, name)
		if command == nil || !shouldSkipCompactReclaimWarning(command) {
			t.Fatalf("daemon command can inspect core store: %s", name)
		}
	}
}
