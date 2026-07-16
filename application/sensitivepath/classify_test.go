package sensitivepath_test

import (
	"strings"
	"testing"

	"github.com/duck8823/traceary/application/sensitivepath"
)

func TestClassify_ShellCommandTextOnlyDotenv(t *testing.T) {
	t.Parallel()

	got := sensitivepath.Classify(sensitivepath.Input{
		Command: "cat .env",
	})
	if !got.Matched {
		t.Fatalf("Matched = false, want true")
	}
	if got.Class != sensitivepath.ClassDotenv {
		t.Fatalf("Class = %q, want dotenv", got.Class)
	}
	if got.Evidence != sensitivepath.EvidenceCommandTextOnly {
		t.Fatalf("Evidence = %q, want command_text_only", got.Evidence)
	}
	if !got.IntentOnly {
		t.Fatal("IntentOnly = false, want true for command-text-only")
	}
	if !strings.Contains(got.Summary, "not proven") {
		t.Fatalf("Summary = %q, want not-proven wording", got.Summary)
	}
	if got.Operation != sensitivepath.OpRead {
		t.Fatalf("Operation = %q, want read", got.Operation)
	}
}

func TestClassify_StructuredFileToolRead(t *testing.T) {
	t.Parallel()

	got := sensitivepath.Classify(sensitivepath.Input{
		Command:  "Read",
		ToolName: "Read",
		Input:    `{"file_path":"/Users/me/project/.env.local"}`,
	})
	if !got.Matched || got.Class != sensitivepath.ClassDotenv {
		t.Fatalf("got %#v, want matched dotenv", got)
	}
	if got.Evidence != sensitivepath.EvidenceStructuredFileTool {
		t.Fatalf("Evidence = %q, want structured_file_tool", got.Evidence)
	}
	if got.IntentOnly {
		t.Fatal("IntentOnly = true, want false for structured file tool")
	}
}

func TestClassify_TruncatedStdoutIsPartialCoverage(t *testing.T) {
	t.Parallel()

	got := sensitivepath.Classify(sensitivepath.Input{
		Command:         "cat ~/.ssh/id_ed25519",
		Output:          "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
		OutputTruncated: true,
	})
	if !got.Matched || got.Class != sensitivepath.ClassSSHKey {
		t.Fatalf("got %#v, want ssh_key match", got)
	}
	if got.Coverage != sensitivepath.CoveragePartial {
		t.Fatalf("Coverage = %q, want partial", got.Coverage)
	}
	if got.CoverageGap != "stdout_truncated" {
		t.Fatalf("CoverageGap = %q, want stdout_truncated", got.CoverageGap)
	}
}

func TestClassify_UnresolvedCustomPattern(t *testing.T) {
	t.Parallel()

	got := sensitivepath.Classify(sensitivepath.Input{
		Command:       "vim secrets/prod-token",
		ExtraPatterns: []string{"prod-token"},
	})
	if !got.Matched || got.Class != sensitivepath.ClassCustom {
		t.Fatalf("got %#v, want custom match", got)
	}
}

func TestClassify_NoMatch(t *testing.T) {
	t.Parallel()

	got := sensitivepath.Classify(sensitivepath.Input{Command: "go test ./..."})
	if got.Matched {
		t.Fatalf("Matched = true for ordinary command: %#v", got)
	}
}

func TestClassify_DoesNotMatchBareEnvOrShellBoilerplate(t *testing.T) {
	t.Parallel()

	// Host shell boilerplate often says "cwd, env vars" without any dotenv path.
	// Matching bare "env" flooded list --sensitive / command_audit.sensitive.
	cases := []string{
		"Shell state (cwd, env vars) persists for subsequent calls",
		"export PATH=$PATH:/usr/local/bin",
		"printenv HOME",
		"env | sort",
		"echo env",
	}
	for _, cmd := range cases {
		got := sensitivepath.Classify(sensitivepath.Input{Command: cmd})
		if got.Matched {
			t.Fatalf("Command %q matched sensitive class %q path %q; want no match", cmd, got.Class, got.MatchedPath)
		}
	}
}

func TestClassify_StillMatchesDotenvPaths(t *testing.T) {
	t.Parallel()

	cases := []string{
		"cat .env",
		"cat .env.local",
		"Read path/to/.env.production",
		`{"file_path":"/Users/me/project/.env"}`,
	}
	for _, cmd := range cases {
		got := sensitivepath.Classify(sensitivepath.Input{Command: cmd, ToolName: "Read"})
		if !got.Matched || got.Class != sensitivepath.ClassDotenv {
			t.Fatalf("Command %q got %#v, want dotenv match", cmd, got)
		}
	}
}

func TestClassifyCommandBody_ParsesAuditShape(t *testing.T) {
	t.Parallel()

	body := "cat .env\n\nINPUT:\n\n\nOUTPUT:\nFOO=bar\n"
	got := sensitivepath.ClassifyCommandBody(body, nil)
	if !got.Matched || got.Class != sensitivepath.ClassDotenv {
		t.Fatalf("got %#v, want dotenv from body", got)
	}
}

func TestClassify_CloudAndBrowserPatterns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		cmd  string
		want sensitivepath.Class
	}{
		{name: "aws", cmd: "cat ~/.aws/credentials", want: sensitivepath.ClassCloudCreds},
		{name: "browser", cmd: "cp ~/Library/Application Support/Google/Chrome/Default/Cookies /tmp/", want: sensitivepath.ClassBrowserProfile},
		{name: "pem", cmd: "openssl x509 -in server.pem -text", want: sensitivepath.ClassKeyMaterial},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := sensitivepath.Classify(sensitivepath.Input{Command: tc.cmd})
			if !got.Matched || got.Class != tc.want {
				t.Fatalf("got %#v, want class %q", got, tc.want)
			}
		})
	}
}
