package cli

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestResolveReadFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		explicit      []string
		explicitSet   bool
		configFields []string
		want          []readFieldID
		wantErr       bool
	}{
		{
			name: "default fallback when nothing is specified",
			want: []readFieldID{readFieldTS, readFieldKind, readFieldSession, readFieldWorkspace, readFieldMessage},
		},
		{
			name:          "config columns used when flag absent",
			configFields: []string{"ts", "kind", "message"},
			want:          []readFieldID{readFieldTS, readFieldKind, readFieldMessage},
		},
		{
			name:          "explicit flag overrides config columns",
			explicit:      []string{"id", "kind"},
			explicitSet:   true,
			configFields: []string{"ts", "kind", "message"},
			want:          []readFieldID{readFieldEventID, readFieldKind},
		},
		{
			name:        "unknown field yields error",
			explicit:    []string{"ts", "bogus", "message"},
			explicitSet: true,
			wantErr:     true,
		},
		{
			name:        "duplicate field yields error",
			explicit:    []string{"ts", "ts", "message"},
			explicitSet: true,
			wantErr:     true,
		},
		{
			name:        "empty field yields error",
			explicit:    []string{"ts", "", "message"},
			explicitSet: true,
			wantErr:     true,
		},
		{
			name:        "explicit empty list is treated as empty (not default)",
			explicit:    []string{},
			explicitSet: true,
			want:        []readFieldID{},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := resolveReadFields(tc.explicit, tc.explicitSet, tc.configFields)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveReadFields() expected error, got nil (fields=%v)", got)
				}
				if !strings.Contains(err.Error(), "read field") {
					t.Fatalf("resolveReadFields() error = %v, want message containing 'read field'", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveReadFields() unexpected error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("resolveReadFields() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestParseReadFields_ErrorIncludesSupportedList(t *testing.T) {
	t.Parallel()

	_, err := parseReadFields([]string{"bogus"})
	if err == nil {
		t.Fatalf("parseReadFields() expected error, got nil")
	}
	if !strings.Contains(err.Error(), "ts") || !strings.Contains(err.Error(), "exit_code") {
		t.Fatalf("error should mention supported field names, got %q", err.Error())
	}
}

func TestResolveReadFieldsForCommand_WideConflict(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI()
	_, err := cli.resolveReadFieldsForCommand([]string{"ts", "kind"}, true, true, false, nil)
	if err == nil {
		t.Fatalf("resolveReadFieldsForCommand() expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "--wide") {
		t.Fatalf("expected error to mention --wide, got %q", err.Error())
	}
}

func TestResolveReadFieldsForCommand_WideWithoutExplicitFieldsIsAllowed(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI()
	got, err := cli.resolveReadFieldsForCommand(nil, false, true, false, nil)
	if err != nil {
		t.Fatalf("resolveReadFieldsForCommand() unexpected error = %v", err)
	}
	// wide mode still returns the default field list so tests downstream
	// do not have to special-case it — the wide branch ignores the field
	// list at the renderer boundary.
	if len(got) == 0 {
		t.Fatalf("resolveReadFieldsForCommand() returned empty fields for wide mode")
	}
}

func TestResolveReadFieldsForCommand_WideSkipsBrokenConfigDefault(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI(WithDefaultReadFields([]string{"bogus"}))
	got, err := cli.resolveReadFieldsForCommand(nil, false, true, false, nil)
	if err != nil {
		t.Fatalf("wide mode should bypass config validation, got err = %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("wide mode should still return default fields, got empty")
	}
}

func TestResolveReadFieldsForCommand_JSONSkipsBrokenConfigDefault(t *testing.T) {
	t.Parallel()

	cli := NewRootCLI(WithDefaultReadFields([]string{"bogus"}))
	got, err := cli.resolveReadFieldsForCommand(nil, false, false, true, nil)
	if err != nil {
		t.Fatalf("--json should bypass config validation, got err = %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("--json mode should still return default fields, got empty")
	}
}

func TestReadFieldsContain(t *testing.T) {
	t.Parallel()

	fields := []readFieldID{readFieldTS, readFieldMessage}
	if !readFieldsContain(fields, readFieldTS) {
		t.Fatalf("readFieldsContain() missed ts")
	}
	if readFieldsContain(fields, readFieldExitCode) {
		t.Fatalf("readFieldsContain() wrongly reported exit_code")
	}
}
