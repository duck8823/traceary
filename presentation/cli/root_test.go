package cli

import "testing"

func TestRootCLI_Command_SilencesCobraErrorOutput(t *testing.T) {
	t.Parallel()

	rootCmd := NewRootCLI(RootCLIOptions{}).Command()
	if !rootCmd.SilenceErrors {
		t.Fatal("rootCmd.SilenceErrors = false, want true")
	}
	if !rootCmd.SilenceUsage {
		t.Fatal("rootCmd.SilenceUsage = false, want true")
	}
}
