package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestResolveColorMode(t *testing.T) {
	tests := []struct {
		name          string
		flagValue     string
		flagSet       bool
		configValue   string
		wideOrJSON    bool
		noColor       bool
		tty           bool
		wantMode      colorMode
		wantErr       bool
		wantErrNeeds  string
	}{
		{
			name:     "auto defaults to on when stdout is a tty",
			tty:      true,
			wantMode: colorModeOn,
		},
		{
			name:     "auto defaults to off when stdout is not a tty",
			tty:      false,
			wantMode: colorModeOff,
		},
		{
			name:      "explicit always turns color on even without a tty",
			flagValue: colorValueAlways,
			flagSet:   true,
			tty:       false,
			wantMode:  colorModeOn,
		},
		{
			name:      "explicit never turns color off even on a tty",
			flagValue: colorValueNever,
			flagSet:   true,
			tty:       true,
			wantMode:  colorModeOff,
		},
		{
			name:        "config always is applied when flag absent",
			configValue: colorValueAlways,
			tty:         false,
			wantMode:    colorModeOn,
		},
		{
			name:      "explicit flag wins over config never",
			flagValue: colorValueAlways,
			flagSet:   true,
			configValue: colorValueNever,
			tty:       false,
			wantMode:  colorModeOn,
		},
		{
			name:      "NO_COLOR env beats flag",
			flagValue: colorValueAlways,
			flagSet:   true,
			noColor:   true,
			tty:       true,
			wantMode:  colorModeOff,
		},
		{
			name:       "wide mode forces color off",
			wideOrJSON: true,
			flagValue:  colorValueAlways,
			flagSet:    true,
			tty:        true,
			wantMode:   colorModeOff,
		},
		{
			name:         "invalid flag value returns error",
			flagValue:    "bogus",
			flagSet:      true,
			wantErr:      true,
			wantErrNeeds: "invalid --color",
		},
		{
			name:         "invalid config value returns error when flag absent",
			configValue:  "bogus",
			tty:          false,
			wantErr:      true,
			wantErrNeeds: "invalid read.color",
		},
		{
			name:        "invalid config is ignored when valid flag overrides it",
			configValue: "bogus",
			flagValue:   colorValueNever,
			flagSet:     true,
			wantMode:    colorModeOff,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.noColor {
				t.Setenv("NO_COLOR", "1")
			} else {
				t.Setenv("NO_COLOR", "")
				// t.Setenv with empty string keeps the variable set to
				// "", which still satisfies LookupEnv. Unset explicitly.
				_ = setenvUnset(t, "NO_COLOR")
			}
			tty := tc.tty
			mode, err := resolveColorMode(tc.flagValue, tc.flagSet, tc.configValue, tc.wideOrJSON, func() bool { return tty })
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveColorMode() expected error, got nil")
				}
				if tc.wantErrNeeds != "" && !strings.Contains(err.Error(), tc.wantErrNeeds) {
					t.Fatalf("resolveColorMode() error = %q, want contains %q", err.Error(), tc.wantErrNeeds)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveColorMode() unexpected error = %v", err)
			}
			if mode != tc.wantMode {
				t.Fatalf("resolveColorMode() = %v, want %v", mode, tc.wantMode)
			}
		})
	}
}

// setenvUnset is a minimal helper that unsets an env variable for the
// duration of a single test, unlike t.Setenv which only sets.
func setenvUnset(t *testing.T, key string) error {
	t.Helper()
	previous, present := lookupEnv(key)
	unsetEnv(key)
	t.Cleanup(func() {
		if present {
			setEnvVar(key, previous)
		} else {
			unsetEnv(key)
		}
	})
	return nil
}

func TestApplyCompactRowHighlight(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		kind        string
		exitCode    int
		exitCodeSet bool
		wantPrefix  string
	}{
		"prompt is cyan":                       {kind: "prompt", wantPrefix: ansiCyan},
		"compact_summary is magenta":           {kind: "compact_summary", wantPrefix: ansiMagenta},
		"session_started is dim":               {kind: "session_started", wantPrefix: ansiDim},
		"session_ended is dim":                 {kind: "session_ended", wantPrefix: ansiDim},
		"failed command_executed is red+bold":  {kind: "command_executed", exitCode: 1, exitCodeSet: true, wantPrefix: ansiRedBold},
		"successful command_executed is plain": {kind: "command_executed", exitCode: 0, exitCodeSet: true, wantPrefix: ""},
		"unknown kind stays plain":             {kind: "note", wantPrefix: ""},
	}

	for name, tc := range tests {
		name, tc := name, tc
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := applyCompactRowHighlight("plain row", tc.kind, tc.exitCode, tc.exitCodeSet)
			if tc.wantPrefix == "" {
				if got != "plain row" {
					t.Fatalf("expected plain row, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, tc.wantPrefix) {
				t.Fatalf("expected prefix %q, got %q", tc.wantPrefix, got)
			}
			if !strings.HasSuffix(got, ansiReset) {
				t.Fatalf("expected reset suffix, got %q", got)
			}
		})
	}
}

func TestIsTerminalWriter_NonFileWriter(t *testing.T) {
	t.Parallel()
	if isTerminalWriter(&bytes.Buffer{}) {
		t.Fatalf("bytes.Buffer should never be reported as a TTY")
	}
}
