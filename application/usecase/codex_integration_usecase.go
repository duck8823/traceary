package usecase

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

// CodexIntegrationUsecase exposes the user-facing local install/uninstall flow
// for the packaged Codex integration.
type CodexIntegrationUsecase interface {
	// Install installs the packaged Codex integration from a repository
	// checkout into the local Codex runtime.
	Install(
		ctx context.Context,
		repoRoot string,
		codexHome string,
		marketplaceRoot string,
		tracearyBin string,
	) (apptypes.CodexIntegrationInstallResult, error)

	// Uninstall removes the packaged Codex integration from the local Codex
	// runtime while preserving unrelated local configuration.
	Uninstall(
		ctx context.Context,
		codexHome string,
		marketplaceRoot string,
	) (apptypes.CodexIntegrationUninstallResult, error)
}
