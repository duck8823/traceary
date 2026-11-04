//go:build darwin || linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	domain "github.com/duck8823/traceary/domain/runtime"
)

func TestClaudeRecoveryEnvironmentRefusesOverrideWithoutChangingInput(t *testing.T) {
	allowed := []string{"HOME=/home/example", "PATH=/usr/bin:/bin", "TERM=xterm", "CLAUDE_CODE_RESUME_INTERRUPTED_TURN=1"}
	if _, err := claudeRecoveryEnvironment(allowed); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{"ANTHROPIC_API_KEY=synthetic", "ANTHROPIC_BASE_URL=synthetic", "CLAUDE_CODE_USE_BEDROCK=0", "CLAUDE_CODE_OAUTH_TOKEN=synthetic", "PYTHONPATH=/other", "DYLD_INSERT_LIBRARIES=/other", "LD_PRELOAD=/other", "CLAUDE_AUTH_TOKEN=synthetic", "HOME=/second", "BROKEN"} {
		t.Run(strings.Split(entry, "=")[0], func(t *testing.T) {
			input := append(append([]string{}, allowed...), entry)
			original := strings.Join(input, "\n")
			if _, err := claudeRecoveryEnvironment(input); !errors.Is(err, domain.ErrUnsupported) {
				t.Fatalf("override accepted: %v", err)
			}
			if strings.Join(input, "\n") != original {
				t.Fatal("trusted environment changed to work around rejection")
			}
		})
	}
}

func TestClaudeRecoveryAuthRequiresExactFirstPartyLoggedInStatus(t *testing.T) {
	good := []byte("{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"apiProvider\":\"firstParty\",\"email\":\"synthetic-account-not-retained\"}")
	if err := decodeClaudeRecoveryAuth(good); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ old, new string }{
		{"true", "false"}, {"true", "null"}, {"claude.ai", "apiKey"}, {"firstParty", "vertex"},
		{"\"loggedIn\":true", "\"loggedIn\":true,\"loggedIn\":false"},
		{"loggedIn", "LoggedIn"}, {"apiProvider", "api_provider"},
		{"\"authMethod\":\"claude.ai\",", ""},
	} {
		if err := decodeClaudeRecoveryAuth([]byte(strings.Replace(string(good), tc.old, tc.new, 1))); err == nil {
			t.Fatal("unknown or ambiguous auth class accepted")
		}
	}
	for _, bad := range []string{"null", "[]", "{}", string(good) + " {}"} {
		if err := decodeClaudeRecoveryAuth([]byte(bad)); err == nil {
			t.Fatal("malformed auth status accepted")
		}
	}
}

func TestNativeSourceFingerprintRetainsExecutableWitnessSemantics(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "native")
	data := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(path, data, 0700); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := inspectNativeSourceFile(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if fingerprint.Digest != hex.EncodeToString(digest[:]) {
		t.Fatal("content digest differs from inspected bytes")
	}
	witness, err := executableWitness(context.Background(), path)
	if err != nil || witness != fingerprint.Witness {
		t.Fatal("registered executable witness changed")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := executableWitness(context.Background(), path); err == nil {
		t.Fatal("nonexecutable source gained launch authority")
	}
	module, err := inspectNativeSourceFile(context.Background(), path, false)
	if err != nil || module.Digest != fingerprint.Digest || module.Witness == fingerprint.Witness {
		t.Fatal("imported source identity ignored permission change")
	}
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectNativeSourceFile(context.Background(), path, false); err == nil {
		t.Fatal("writable imported routing source accepted")
	}
}

func TestClaudeRecoveryUnknownSourceNeverExecutesProviderOrChangesRoute(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "native")
	marker := filepath.Join(dir, "must-not-run")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ntouch "+nativeShellLiteral(marker)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	witness, err := executableWitness(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := domain.HostProfileFrom(domain.HostProfileState{ID: "claude", Host: "claude", Executable: path, ExecutableWitness: witness, Version: claudeRecoveryVersion + " (Claude Code)"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspectClaudeRecoverySource(context.Background(), profile, []string{"HOME=" + dir}); !errors.Is(err, domain.ErrUnsupported) {
		t.Fatalf("unqualified program accepted: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("static source check executed an unqualified native program")
	}
}

func TestClaudeRecoveryProviderProbeUsesOnlyNativeAuthStatusWithoutInput(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "native")
	script := "#!/bin/sh\n[ \"$#\" = 2 ] && [ \"$1\" = auth ] && [ \"$2\" = status ] || exit 91\nif read -r input; then exit 92; fi\nprintf '%s' '{\"loggedIn\":true,\"authMethod\":\"claude.ai\",\"apiProvider\":\"firstParty\"}'\nprintf '%s' 'synthetic account stderr discarded' >&2\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	source := claudeRecoverySource{Executable: path}
	if err := verifyClaudeRecoveryProvider(context.Background(), source, []string{"HOME=" + dir}, dir); err != nil {
		t.Fatal(err)
	}
	// The fake is a port-level protocol test only, not a qualified native source.
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s' '{\"loggedIn\":true,\"authMethod\":\"apiKey\",\"apiProvider\":\"firstParty\"}'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := verifyClaudeRecoveryProvider(context.Background(), source, []string{"HOME=" + dir}, dir); err == nil {
		t.Fatal("alternate auth accepted")
	}
}
