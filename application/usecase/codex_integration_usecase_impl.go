package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	apptypes "github.com/duck8823/traceary/application/types"
)

type codexIntegrationUsecase struct {
	manager application.CodexIntegrationManager
}

// NewCodexIntegrationUsecase creates a CodexIntegrationUsecase.
func NewCodexIntegrationUsecase(manager application.CodexIntegrationManager) CodexIntegrationUsecase {
	return &codexIntegrationUsecase{manager: manager}
}

func (u *codexIntegrationUsecase) Install(
	ctx context.Context,
	repoRoot string,
	codexHome string,
	marketplaceRoot string,
	tracearyBin string,
) (apptypes.CodexIntegrationInstallResult, error) {
	if strings.TrimSpace(repoRoot) == "" {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("repository root must not be empty")
	}
	if strings.TrimSpace(codexHome) == "" {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("codex home must not be empty")
	}
	if strings.TrimSpace(marketplaceRoot) == "" {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("marketplace root must not be empty")
	}
	if strings.TrimSpace(tracearyBin) == "" {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("traceary binary must not be empty")
	}

	result, err := u.manager.Install(
		ctx,
		strings.TrimSpace(repoRoot),
		strings.TrimSpace(codexHome),
		strings.TrimSpace(marketplaceRoot),
		strings.TrimSpace(tracearyBin),
	)
	if err != nil {
		return apptypes.CodexIntegrationInstallResult{}, xerrors.Errorf("failed to install Codex integration: %w", err)
	}

	return result, nil
}

func (u *codexIntegrationUsecase) Uninstall(
	ctx context.Context,
	codexHome string,
	marketplaceRoot string,
) (apptypes.CodexIntegrationUninstallResult, error) {
	if strings.TrimSpace(codexHome) == "" {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("codex home must not be empty")
	}
	if strings.TrimSpace(marketplaceRoot) == "" {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("marketplace root must not be empty")
	}

	result, err := u.manager.Uninstall(
		ctx,
		strings.TrimSpace(codexHome),
		strings.TrimSpace(marketplaceRoot),
	)
	if err != nil {
		return apptypes.CodexIntegrationUninstallResult{}, xerrors.Errorf("failed to uninstall Codex integration: %w", err)
	}

	return result, nil
}
