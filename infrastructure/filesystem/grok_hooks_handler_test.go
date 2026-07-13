package filesystem_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/infrastructure/filesystem"
)

func TestGrokHooksHandler_ExposesFailClosedBoundary(t *testing.T) {
	t.Parallel()

	handler := filesystem.NewGrokHooksHandler()
	if got := handler.Name(); got != "grok" {
		t.Fatalf("Name() = %q, want grok", got)
	}
	if got := handler.Build("traceary").Events(); len(got) != 0 {
		t.Fatalf("Build().Events() = %v, want empty until runtime support lands", got)
	}
	if _, err := handler.DefaultInstallPath(t.TempDir()); err == nil || !strings.Contains(err.Error(), "native runtime support") {
		t.Fatalf("DefaultInstallPath() error = %v, want fail-closed runtime support error", err)
	}
}
