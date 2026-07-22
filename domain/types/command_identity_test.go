package types_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/types"
)

func TestCommandIdentityFrom(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		raw         string
		wantWrapper string
		wantCommand string
	}{
		{name: "direct executable", raw: "/usr/bin/git status", wantCommand: "git"},
		{name: "rtk wrapper", raw: "rtk git status", wantWrapper: "rtk", wantCommand: "git"},
		{name: "rtk proxy", raw: "rtk proxy git diff", wantWrapper: "rtk", wantCommand: "git"},
		{name: "quoted executable", raw: `'rtk' "/usr/bin/find" .`, wantWrapper: "rtk", wantCommand: "find"},
		{name: "rtk without command", raw: "rtk", wantWrapper: "rtk", wantCommand: "unknown"},
		{name: "rtk option is not executable", raw: "rtk --help", wantWrapper: "rtk", wantCommand: "unknown"},
		{name: "rtk variable command is unknown", raw: `rtk "$COMMAND"`, wantWrapper: "rtk", wantCommand: "unknown"},
		{name: "rtk command substitution is unknown", raw: "rtk $(command)", wantWrapper: "rtk", wantCommand: "unknown"},
		{name: "rtk backtick substitution is unknown", raw: "rtk `command`", wantWrapper: "rtk", wantCommand: "unknown"},
		{name: "unclosed quote is unknown", raw: `rtk "git`, wantCommand: "unknown"},
		{name: "shell operator in executable is unknown", raw: "git&& echo unsafe", wantCommand: "unknown"},
		{name: "empty is unknown", raw: "  ", wantCommand: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := types.CommandIdentityFrom(tc.raw)
			wrapper := ""
			if value, ok := got.Wrapper().Value(); ok {
				wrapper = value.String()
			}
			if wrapper != tc.wantWrapper {
				t.Fatalf("Wrapper() = %q, want %q", wrapper, tc.wantWrapper)
			}
			if command := got.Command().String(); command != tc.wantCommand {
				t.Fatalf("Command() = %q, want %q", command, tc.wantCommand)
			}
		})
	}
}
