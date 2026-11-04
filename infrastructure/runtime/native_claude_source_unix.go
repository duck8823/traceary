//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
)

const claudeRecoveryNativeDigest = "677a2ea7bf576cedd20e191d973914ed8a41796f53a63b1923381ddc4d5b5e7d"
const claudeRecoveryEntryDigest = "079234054a9e035839f50118b618c1568c8ae1e20950f9b668a60bf27722e3cc"
const claudeRecoveryRouterDigest = "c9af398d4f3dc015572786e0ccb5e8944ab758d618f44b6fe792100cfb602b18"
const claudeRecoveryVerifierDigest = "32be0b7e7f9c076898fbbca3c926d0269e0effb8aa807087debffb83192c875f"

type claudeRecoverySource struct {
	Namespace string
	// This is the operator's original entry, never a substitute native command.
	Executable string
}

func claudeRecoveryEnvironment(environment []string) (map[string]string, error) {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			return nil, domain.ErrUnsupported
		}
		if _, duplicate := values[key]; duplicate {
			return nil, domain.ErrUnsupported
		}
		values[key] = value
		// Refuse alternate route/auth/injection configuration rather than changing
		// it or trying another engine. The ordinary launch remains unaffected.
		if strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "PYTHON") ||
			strings.HasPrefix(key, "LD_") || strings.HasPrefix(key, "DYLD_") ||
			strings.HasPrefix(key, "CLAUDE_CODE_") && key != "CLAUDE_CODE_RESUME_INTERRUPTED_TURN" ||
			key == "CLAUDE_API_KEY" || key == "CLAUDE_AUTH_TOKEN" {
			return nil, domain.ErrUnsupported
		}
	}
	if !canonicalNativePath(values["HOME"]) {
		return nil, domain.ErrUnsupported
	}
	return values, nil
}

// inspectClaudeRecoverySource does not execute auth, read history, create a
// conversation or change the selected entry. Normal launches may use only this
// static optional preflight; provider/history work belongs after actual Wait or
// explicit recovery inspection. Unknown source/version is simply unsupported.
func inspectClaudeRecoverySource(ctx context.Context, profile domain.HostProfile, environment []string) (claudeRecoverySource, error) {
	p := profile.Snapshot()
	if p.Host != "claude" || p.Version != claudeRecoveryVersion+" (Claude Code)" {
		return claudeRecoverySource{}, domain.ErrUnsupported
	}
	env, err := claudeRecoveryEnvironment(environment)
	if err != nil {
		return claudeRecoverySource{}, err
	}
	entry, err := inspectNativeSourceFile(ctx, p.Executable, true)
	if err != nil {
		return claudeRecoverySource{}, err
	}
	if entry.Witness != p.ExecutableWitness {
		return claudeRecoverySource{}, domain.ErrChanged
	}
	switch entry.Digest {
	case claudeRecoveryNativeDigest:
		// An operator may explicitly register this exact native artifact. Never
		// rewrite a different registered entry to it.
	case claudeRecoveryEntryDigest:
		root := filepath.Join(env["HOME"], ".local", "lib", "claude-print-wif", "bin")
		if p.Executable != filepath.Join(root, "claude") {
			return claudeRecoverySource{}, domain.ErrUnsupported
		}
		for name, digest := range map[string]string{"claude_router.py": claudeRecoveryRouterDigest, "verify_wif.py": claudeRecoveryVerifierDigest} {
			imported, err := inspectNativeSourceFile(ctx, filepath.Join(root, name), false)
			if err != nil {
				return claudeRecoverySource{}, err
			}
			if imported.Digest != digest {
				return claudeRecoverySource{}, domain.ErrChanged
			}
		}
		// The pinned router's non-print route uses this existing resolution.
		// Verify it; do not execute the resolved file instead of the selected entry.
		native, err := filepath.EvalSymlinks(filepath.Join(env["HOME"], ".local", "bin", "claude"))
		if err != nil {
			return claudeRecoverySource{}, domain.ErrUnsafe
		}
		artifact, err := inspectNativeSourceFile(ctx, native, true)
		if err != nil {
			return claudeRecoverySource{}, err
		}
		if artifact.Digest != claudeRecoveryNativeDigest {
			return claudeRecoverySource{}, domain.ErrChanged
		}
	default:
		return claudeRecoverySource{}, domain.ErrUnsupported
	}
	config := env["CLAUDE_CONFIG_DIR"]
	if config == "" {
		config = filepath.Join(env["HOME"], ".claude")
	}
	if !canonicalNativePath(config) {
		return claudeRecoverySource{}, domain.ErrUnsupported
	}
	// Do not follow a changed configured namespace or invent a projects root.
	namespace := filepath.Join(config, "projects")
	fd, err := privateRuntimeDir(namespace, false)
	if err != nil {
		return claudeRecoverySource{}, err
	}
	_ = unix.Close(fd)
	return claudeRecoverySource{Namespace: namespace, Executable: p.Executable}, nil
}

func decodeClaudeRecoveryAuth(data []byte) error {
	fields, err := nativeHistoryFields(data, "loggedIn", "authMethod", "apiProvider")
	if err != nil {
		return err
	}
	var loggedIn bool
	if json.Unmarshal(fields["loggedIn"], &loggedIn) != nil || !loggedIn {
		return domain.ErrUnsupported
	}
	method, err := nativeHistoryString(fields, "authMethod")
	if err != nil {
		return err
	}
	provider, err := nativeHistoryString(fields, "apiProvider")
	if err != nil {
		return err
	}
	if method != "claude.ai" || provider != "firstParty" {
		return domain.ErrUnsupported
	}
	return nil
}

// verifyClaudeRecoveryProvider lets only the native CLI consume host auth.
// Account/credential fields and stderr are discarded; neither errors nor
// diagnostics contain the borrowed response. No login, print/API mode or retry.
func verifyClaudeRecoveryProvider(ctx context.Context, source claudeRecoverySource, environment []string, cwd string) error {
	if !canonicalNativePath(source.Executable) || !canonicalNativePath(cwd) {
		return domain.ErrInvalid
	}
	if _, err := claudeRecoveryEnvironment(environment); err != nil {
		return err
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	command := exec.CommandContext(probe, source.Executable, "auth", "status")
	command.Dir = cwd
	command.Env = launchEnvironment(environment, "", domain.LaunchIdentity{})
	command.Stderr = io.Discard
	command.WaitDelay = 200 * time.Millisecond
	var output boundedVersionOutput
	command.Stdout = &output
	runErr := command.Run()
	output.mu.Lock()
	defer output.mu.Unlock()
	defer clear(output.data)
	if runErr != nil || output.overflow || probe.Err() != nil {
		return domain.ErrUnsupported
	}
	// This CLI's minimized status is small; the shared bounded writer refuses
	// larger account/status output rather than retaining or logging it.
	return decodeClaudeRecoveryAuth(bytes.TrimSpace(output.data))
}
