package filesystem

import (
	"strings"
	"testing"
)

func TestKimiHooksHandler_ExposesFailClosedBoundary(t *testing.T) {
	t.Parallel()

	handler := NewKimiHooksHandler()
	if got := handler.Name(); got != "kimi" {
		t.Fatalf("Name() = %q, want kimi", got)
	}
	if got := handler.Build("traceary").Events(); len(got) != 0 {
		t.Fatalf("Build().Events() = %v, want empty until runtime support lands", got)
	}
	if _, err := handler.DefaultInstallPath(t.TempDir()); err == nil || !strings.Contains(err.Error(), "native runtime support") {
		t.Fatalf("DefaultInstallPath() error = %v, want fail-closed runtime support error", err)
	}
	if err := handler.validateInstall(); err == nil || !strings.Contains(err.Error(), "native runtime support") {
		t.Fatalf("validateInstall() error = %v, want fail-closed runtime support error", err)
	}
}
