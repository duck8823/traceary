//go:build darwin || linux

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestNativeMetadataSettingsContainOneSilentNonRecordingHook(t *testing.T) {
	plan, _, _ := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	dir := t.TempDir()
	executable := filepath.Join(dir, "native ' $(false); cli")
	argsPath := filepath.Join(dir, "args")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + nativeShellLiteral(argsPath) + "\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	path, err := slot.writeSettings(ctx, executable)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings nativeMetadataSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if len(settings.Hooks.SessionStart) != 1 || len(settings.Hooks.SessionStart[0].Hooks) != 1 {
		t.Fatal("duplicate startup recorder")
	}
	hook := settings.Hooks.SessionStart[0].Hooks[0]
	if hook.Type != "command" || hook.Timeout != 6 || strings.Contains(hook.Command, "hook transcript") {
		t.Fatal("not a distinct metadata hook")
	}
	// Execute only a synthetic local script, never a native AI or user shell.
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", hook.Command).CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("optional hook emitted output or failed: %q %v", out, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "runtime\nnative-session-start\n--slot\n" + slot.metadata.root + "\n"
	if string(args) != want {
		t.Fatalf("shell metacharacters changed arguments: %q", args)
	}
	info, _ := os.Stat(path)
	if _, err := slot.writeSettings(ctx, executable); err != nil {
		t.Fatal(err)
	}
	again, _ := os.Stat(path)
	if !os.SameFile(info, again) {
		t.Fatal("identical settings rewritten")
	}
	if _, err := slot.writeSettings(ctx, filepath.Join(dir, "other")); !errors.Is(err, domain.ErrChanged) {
		t.Fatalf("prepared executable silently changed: %v", err)
	}
	if _, err := slot.writeSettings(ctx, "/invalid\npath"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("control character accepted: %v", err)
	}
}

func TestNativeMetadataSettingsFailureNeverProducesPermissionOutput(t *testing.T) {
	plan, _, _ := nativeStartFixture()
	ctx := context.Background()
	slot, err := newNativeStartSlot(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = slot.close(ctx) }()
	path, err := slot.writeSettings(ctx, "/nonexistent-native-metadata-helper")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var settings nativeMetadataSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(ctx, "/bin/sh", "-c", settings.Hooks.SessionStart[0].Hooks[0].Command).CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Fatalf("optional helper error escaped: %q %v", out, err)
	}
	if _, err := slot.read(ctx); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("failed helper fabricated source: %v", err)
	}
}
