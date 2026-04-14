package application

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CodexIntegrationManager manages the local Codex plugin/runtime installation
// that Traceary ships from a repository checkout.
type CodexIntegrationManager interface {
	// Install installs the packaged Codex plugin into the local marketplace,
	// the active Codex plugin cache, config.toml, and hooks.json.
	Install(
		ctx context.Context,
		repoRoot string,
		codexHome string,
		marketplaceRoot string,
		tracearyBin string,
	) (apptypes.CodexIntegrationInstallResult, error)

	// Uninstall removes the Traceary-managed Codex plugin cache, config entry,
	// and hooks while preserving unrelated local Codex settings.
	Uninstall(
		ctx context.Context,
		codexHome string,
		marketplaceRoot string,
	) (apptypes.CodexIntegrationUninstallResult, error)
}
