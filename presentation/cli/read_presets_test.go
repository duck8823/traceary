package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/duck8823/traceary/presentation"
)

func TestResolveReadPreset_BuiltinFailures(t *testing.T) {
	t.Parallel()

	preset, ok, err := resolveReadPreset("failures", nil, nil)
	if err != nil {
		t.Fatalf("resolveReadPreset() unexpected error = %v", err)
	}
	if !ok {
		t.Fatalf("resolveReadPreset() ok = false, want true")
	}
	if !preset.filters.failuresSet || !preset.filters.failures {
		t.Fatalf("expected failures filter to be set true, got %+v", preset.filters)
	}
	if !readFieldsContain(preset.fields, readFieldExitCode) {
		t.Fatalf("expected failures preset to include exit_code field, got %v", preset.fields)
	}
}

func TestResolveReadPreset_BuiltinPromptsOnly(t *testing.T) {
	t.Parallel()

	preset, _, err := resolveReadPreset("prompts-only", nil, nil)
	if err != nil {
		t.Fatalf("resolveReadPreset() unexpected error = %v", err)
	}
	if preset.filters.kind != "prompt" || !preset.filters.kindSet {
		t.Fatalf("expected kind=prompt, got %+v", preset.filters)
	}
	if len(preset.fields) != 0 {
		t.Fatalf("expected prompts-only to not override fields, got %v", preset.fields)
	}
}

func TestResolveReadPreset_UnknownReturnsError(t *testing.T) {
	t.Parallel()

	_, _, err := resolveReadPreset("bogus", nil, nil)
	if err == nil {
		t.Fatalf("resolveReadPreset() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "failures") {
		t.Fatalf("error should mention built-in preset catalog, got %q", err.Error())
	}
}

func TestResolveReadPreset_EmptyReturnsNotOK(t *testing.T) {
	t.Parallel()

	preset, ok, err := resolveReadPreset("", nil, nil)
	if err != nil {
		t.Fatalf("resolveReadPreset() unexpected error = %v", err)
	}
	if ok {
		t.Fatalf("resolveReadPreset() ok = true, want false for empty name")
	}
	_ = preset
}

func TestResolveReadPreset_UserDefinedOverridesBuiltin(t *testing.T) {
	t.Parallel()

	userFields := []string{"ts", "kind"}
	userDefined := map[string]presentation.ReadPreset{
		"failures": {Fields: userFields},
	}
	warn := &bytes.Buffer{}

	preset, ok, err := resolveReadPreset("failures", userDefined, warn)
	if err != nil {
		t.Fatalf("resolveReadPreset() unexpected error = %v", err)
	}
	if !ok {
		t.Fatalf("resolveReadPreset() ok = false, want true")
	}
	if len(preset.fields) != 2 || preset.fields[0] != readFieldTS || preset.fields[1] != readFieldKind {
		t.Fatalf("user-defined override did not win, got %v", preset.fields)
	}
	if !strings.Contains(warn.String(), "[WARN]") {
		t.Fatalf("expected collision warning on stderr, got %q", warn.String())
	}
}

func TestResolveReadPreset_UserDefinedWithInvalidFieldRejected(t *testing.T) {
	t.Parallel()

	userDefined := map[string]presentation.ReadPreset{
		"my-view": {Fields: []string{"ts", "bogus"}},
	}
	_, _, err := resolveReadPreset("my-view", userDefined, nil)
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "my-view") {
		t.Fatalf("error should name the offending preset, got %q", err.Error())
	}
}

func TestResolveReadPreset_UserDefinedWithInvalidKindRejected(t *testing.T) {
	t.Parallel()

	userDefined := map[string]presentation.ReadPreset{
		"my-view": {Filters: presentation.ReadPresetFilters{Kind: "bogus-kind"}},
	}
	_, _, err := resolveReadPreset("my-view", userDefined, nil)
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
}

func TestApplyReadPresetToListInput_RespectsExplicitOverride(t *testing.T) {
	t.Parallel()

	input := listCommandInput{
		kind:    "command_executed",
		kindSet: true,
	}
	preset, _, err := resolveReadPreset("prompts-only", nil, nil)
	if err != nil {
		t.Fatalf("resolveReadPreset() error = %v", err)
	}
	applyReadPresetToListInput(&input, preset)
	if input.kind != "command_executed" {
		t.Fatalf("explicit kind was overwritten by preset, got %q", input.kind)
	}
}

func TestApplyReadPresetToListInput_FillsUnsetFilters(t *testing.T) {
	t.Parallel()

	input := listCommandInput{}
	preset, _, err := resolveReadPreset("failures", nil, nil)
	if err != nil {
		t.Fatalf("resolveReadPreset() error = %v", err)
	}
	applyReadPresetToListInput(&input, preset)
	if !input.failuresOnly {
		t.Fatalf("failures filter not applied, got %+v", input)
	}
}

func TestResolveReadFieldsForCommand_PresetFieldsTakePrecedenceOverConfig(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI(WithDefaultReadFields([]string{"ts", "message"}))
	presetFields := []readFieldID{readFieldTS, readFieldKind, readFieldExitCode, readFieldMessage}

	got, err := cli.resolveReadFieldsForCommand(nil, false, false, false, presetFields)
	if err != nil {
		t.Fatalf("resolveReadFieldsForCommand() error = %v", err)
	}
	if len(got) != len(presetFields) || got[2] != readFieldExitCode {
		t.Fatalf("expected preset fields to take precedence over config default, got %v", got)
	}
}

func TestResolveReadFieldsForCommand_ExplicitFieldsOverridePreset(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI()
	presetFields := []readFieldID{readFieldTS, readFieldKind, readFieldMessage}

	got, err := cli.resolveReadFieldsForCommand([]string{"id"}, true, false, false, presetFields)
	if err != nil {
		t.Fatalf("resolveReadFieldsForCommand() error = %v", err)
	}
	if len(got) != 1 || got[0] != readFieldEventID {
		t.Fatalf("explicit --fields should override preset, got %v", got)
	}
}
