package cli

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWithDoctorInspectedDBPath(t *testing.T) {
	t.Parallel()
	const dbPath = "/store/custom.db"
	quoted := shellQuote(dbPath)
	cases := []struct {
		name    string
		command string
		want    string
	}{
		{name: "empty command", command: "", want: ""},
		{name: "doctor fix", command: "traceary doctor --fix", want: "traceary doctor --fix --db-path " + quoted},
		{name: "already has db-path", command: "traceary doctor --fix --db-path /other.db", want: "traceary doctor --fix --db-path /other.db"},
		{name: "memory activate", command: "traceary memory admin activate --target claude --apply", want: "traceary memory admin activate --target claude --apply --db-path " + quoted},
		{name: "store compact", command: "traceary store compact", want: "traceary store compact --db-path " + quoted},
		{name: "host-only command", command: "claude plugin update traceary@traceary-plugins", want: "claude plugin update traceary@traceary-plugins"},
		{name: "which", command: "which -a traceary", want: "which -a traceary"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := withDoctorInspectedDBPath(tc.command, dbPath); got != tc.want {
				t.Fatalf("withDoctorInspectedDBPath(%q) = %q, want %q", tc.command, got, tc.want)
			}
		})
	}
}

func TestWithDoctorInspectedDBPath_EmptyStore(t *testing.T) {
	t.Parallel()
	const command = "traceary doctor --fix"
	if got := withDoctorInspectedDBPath(command, ""); got != command {
		t.Fatalf("empty db path rewrote command: %q", got)
	}
}

func TestRewriteBacktickedStoreCommands(t *testing.T) {
	t.Parallel()
	input := "preview with `traceary doctor --fix --dry-run`, then `claude plugin update x`"
	got := rewriteBacktickedStoreCommands(input, "/store/custom.db")
	if !strings.Contains(got, "`traceary doctor --fix --dry-run --db-path "+shellQuote("/store/custom.db")+"`") {
		t.Fatalf("did not rewrite doctor command: %q", got)
	}
	if !strings.Contains(got, "`claude plugin update x`") {
		t.Fatalf("rewrote host command: %q", got)
	}
}

func TestAnnotateDoctorScopeAndDBPathHints(t *testing.T) {
	t.Parallel()
	report := &doctorReport{
		hintDBPath: "/store/custom.db",
		Checks: []doctorCheck{
			{
				Name:       "hook-state-residue",
				Message:    "no leftover hook state, diagnostics, or ended markers",
				Hint:       "run `traceary doctor --fix`",
				FixCommand: "traceary doctor --fix",
			},
			{
				Name:       "codex-memory-activation",
				Message:    "missing. Preview with `traceary memory admin activate --target codex --dry-run --diff`",
				FixCommand: "traceary memory admin activate --target codex --apply",
			},
			{
				Name:       "claude-plugin-cache",
				Message:    "plugin cache is current",
				FixCommand: "claude plugin update traceary@traceary-plugins",
			},
		},
	}
	annotateDoctorScopeAndDBPathHints(report)

	residue := report.Checks[0]
	if !strings.Contains(residue.Message, doctorStoreIndependentLabel) {
		t.Fatalf("store-independent check unlabeled: %q", residue.Message)
	}
	if residue.FixCommand != "traceary doctor --fix --db-path "+shellQuote("/store/custom.db") {
		t.Fatalf("residue FixCommand = %q", residue.FixCommand)
	}
	if !strings.Contains(residue.Hint, "--db-path") {
		t.Fatalf("residue hint missing db-path: %q", residue.Hint)
	}

	activation := report.Checks[1]
	if strings.Contains(activation.Message, doctorStoreIndependentLabel) {
		t.Fatalf("store-scoped check labeled independent: %q", activation.Message)
	}
	if !strings.Contains(activation.FixCommand, "--db-path") {
		t.Fatalf("activation FixCommand = %q", activation.FixCommand)
	}

	cache := report.Checks[2]
	if cache.FixCommand != "claude plugin update traceary@traceary-plugins" {
		t.Fatalf("host FixCommand rewritten: %q", cache.FixCommand)
	}
	if !strings.Contains(cache.Message, doctorStoreIndependentLabel) {
		t.Fatalf("plugin cache unlabeled: %q", cache.Message)
	}
}

func TestDoctorDBPathWasExplicit(t *testing.T) {
	t.Setenv(dbPathEnvKey, "")
	if doctorDBPathWasExplicit("") {
		t.Fatal("default home store must not be explicit")
	}
	if !doctorDBPathWasExplicit("/tmp/x.db") {
		t.Fatal("flag path must be explicit")
	}
	t.Setenv(dbPathEnvKey, filepath.Join(t.TempDir(), "env.db"))
	if !doctorDBPathWasExplicit("") {
		t.Fatal("TRACEARY_DB_PATH must be explicit")
	}
}
