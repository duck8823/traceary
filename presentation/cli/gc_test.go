package cli_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_GCCommandRemoved(t *testing.T) {
	root := cli.NewRootCLI().Command()
	root.SetArgs([]string{"store", "gc"})
	err := root.Execute()
	if err == nil {
		t.Fatal("store gc must be unknown after #1872")
	}
	if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "gc") {
		t.Fatalf("Execute() error = %v, want unknown command", err)
	}
}
