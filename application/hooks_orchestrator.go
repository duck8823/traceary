package application

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// HooksOrchestrator is the application-level entrypoint for hook generation
// and installation. It resolves the correct HooksClientHandler for a client
// alias, builds the Hooks aggregate, and renders or writes it to disk.
type HooksOrchestrator interface {
	// Generate returns the rendered hook configuration bytes for the given
	// client. tracearyBin is the command or path used to launch the traceary
	// binary.
	Generate(ctx context.Context, client string, tracearyBin string) ([]byte, error)

	// GenerateWithMatcher is the matcher-aware variant of Generate. The
	// matcher preset only affects clients that honor a preset (Claude
	// Code today); other clients ignore the value. Empty string means
	// "use the client's default matcher set" and is equivalent to
	// calling Generate directly.
	GenerateWithMatcher(ctx context.Context, client string, tracearyBin string, matcherPreset string) ([]byte, error)

	// Install writes the hook configuration file for the given client.
	// outputPath overrides the default install path when present. force
	// overwrites the existing file instead of merging with it.
	// The returned string is the resolved absolute path that was written.
	Install(
		ctx context.Context,
		client string,
		tracearyBin string,
		projectDir string,
		outputPath types.Optional[string],
		force bool,
	) (string, error)

	// InstallWithMatcher is the matcher-aware variant of Install. The
	// matcher preset only affects clients that honor a preset (Claude
	// Code today); other clients ignore the value. Callers should pass
	// the empty string to request the client's default matcher set,
	// which is equivalent to calling Install directly.
	InstallWithMatcher(
		ctx context.Context,
		client string,
		tracearyBin string,
		projectDir string,
		outputPath types.Optional[string],
		force bool,
		matcherPreset string,
	) (string, error)

	// SupportedClients returns the canonical list of supported client
	// identifiers (e.g. "claude", "codex", "gemini").
	SupportedClients() []string

	// NormalizeClient resolves a client alias (e.g. "claude-code") to its
	// canonical identifier (e.g. "claude").
	NormalizeClient(client string) (string, error)

	// ResolveInstallPath returns the configuration file path that Install
	// would write to for the given client and project directory. When
	// outputPath is present it overrides the default install path.
	ResolveInstallPath(
		client string,
		projectDir string,
		outputPath types.Optional[string],
	) (string, error)
}
