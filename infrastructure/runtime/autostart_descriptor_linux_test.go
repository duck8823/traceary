package runtime_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

func TestAutoStartDescriptorUnsupportedPlatformNeverMutatesPreference(t *testing.T) {
	root := daemonRoot(t)
	ctx := context.Background()
	service, err := (infra.LocalOperatorFactory{}).CreateAutoStart(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.SetOptIn(ctx, true); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.Descriptor(ctx)
	if !errors.Is(err, app.ErrRuntimeUnsupported) || value != "" {
		t.Fatal("unsupported descriptor accepted", err)
	}
	after, err := os.ReadFile(filepath.Join(root, "lifecycle.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("unsupported render wrote preference", err)
	}
}
