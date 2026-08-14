package cli_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation/cli"
)

func TestRootCLI_StoreDedupeRemoved(t *testing.T) {
	root := cli.NewRootCLI().Command()
	root.SetArgs([]string{"store", "dedupe", "content-events"})
	err := root.Execute()
	if err == nil {
		t.Fatal("store dedupe must be unknown after #1872")
	}
	if !strings.Contains(err.Error(), "unknown") && !strings.Contains(err.Error(), "dedupe") {
		t.Fatalf("Execute() error = %v, want unknown command", err)
	}
}
