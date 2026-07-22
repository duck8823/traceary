package usecase

import (
	"context"
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/queryservice"
	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// WorkspaceIdentityUsecase exposes body-free reports and reviewed alias changes.
type WorkspaceIdentityUsecase interface {
	Report(ctx context.Context, conflictSampleLimit int) (apptypes.WorkspaceIdentityReport, error)
	AddAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, reviewedBy, note string) error
	RemoveAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace) error
}

type workspaceIdentityUsecase struct {
	queries queryservice.WorkspaceIdentityQueryService
	aliases model.WorkspaceAliasRepository
	clock   types.Clock
}

// NewWorkspaceIdentityUsecase constructs workspace identity reporting and alias orchestration.
func NewWorkspaceIdentityUsecase(queries queryservice.WorkspaceIdentityQueryService, aliases model.WorkspaceAliasRepository, clock types.Clock) WorkspaceIdentityUsecase {
	if clock == nil {
		clock = types.SystemClock{}
	}
	return &workspaceIdentityUsecase{queries: queries, aliases: aliases, clock: clock}
}

func (u *workspaceIdentityUsecase) Report(ctx context.Context, conflictSampleLimit int) (apptypes.WorkspaceIdentityReport, error) {
	if u.queries == nil {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("workspace identity query service is not configured")
	}
	if conflictSampleLimit < 0 {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("conflict sample limit must not be negative")
	}
	report, err := u.queries.WorkspaceIdentityReport(ctx, conflictSampleLimit)
	if err != nil {
		return apptypes.WorkspaceIdentityReport{}, xerrors.Errorf("failed to query workspace identity report: %w", err)
	}
	return report, nil
}

func (u *workspaceIdentityUsecase) AddAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace, reviewedBy, note string) error {
	if u.aliases == nil {
		return xerrors.Errorf("workspace alias repository is not configured")
	}
	alias, err := model.NewWorkspaceAlias(sessionID, workspace, u.clock.Now(), reviewedBy, note)
	if err != nil {
		return xerrors.Errorf("failed to validate workspace alias: %w", err)
	}
	if err := u.aliases.SaveWorkspaceAlias(ctx, alias); err != nil {
		return xerrors.Errorf("failed to save workspace alias: %w", err)
	}
	return nil
}

func (u *workspaceIdentityUsecase) RemoveAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace) error {
	if u.aliases == nil {
		return xerrors.Errorf("workspace alias repository is not configured")
	}
	resolvedSessionID, err := types.SessionIDFrom(sessionID.String())
	if err != nil {
		return xerrors.Errorf("invalid workspace alias session ID: %w", err)
	}
	resolvedWorkspace := types.Workspace(strings.TrimSpace(workspace.String()))
	if resolvedWorkspace.String() == "" {
		return xerrors.Errorf("workspace alias must not be empty")
	}
	if err := u.aliases.DeleteWorkspaceAlias(ctx, resolvedSessionID, resolvedWorkspace); err != nil {
		return xerrors.Errorf("failed to delete workspace alias: %w", err)
	}
	return nil
}
