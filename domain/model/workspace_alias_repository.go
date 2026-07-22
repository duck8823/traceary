package model

import (
	"context"

	"github.com/duck8823/traceary/domain/types"
)

// WorkspaceAliasRepository persists explicit reviewed alias decisions.
type WorkspaceAliasRepository interface {
	SaveWorkspaceAlias(ctx context.Context, alias *WorkspaceAlias) error
	DeleteWorkspaceAlias(ctx context.Context, sessionID types.SessionID, workspace types.Workspace) error
}
