package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/types"
)

func (c *RootCLI) initializeWorkspaceIdentityStore(ctx context.Context, dbPath string) error {
	if c.workspaceIdentity == nil || c.storeManagement == nil {
		return xerrors.Errorf("%s", Localize("workspace identity/store management usecase is not configured", "workspace identity/store management usecase が設定されていません"))
	}
	resolved, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to resolve DB path", "DB パスの解決に失敗しました"), err)
	}
	c.applyDatabasePath(resolved)
	if err := c.storeManagement.Initialize(ctx); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to initialize store", "store の初期化に失敗しました"), err)
	}
	return nil
}

func (c *RootCLI) runStoreWorkspaceAliasAdd(ctx context.Context, output io.Writer, dbPath, sessionID, workspace, reviewer, note string) error {
	if err := c.initializeWorkspaceIdentityStore(ctx, dbPath); err != nil {
		return err
	}
	if err := c.workspaceIdentity.AddAlias(ctx, types.SessionID(sessionID), types.Workspace(workspace), reviewer, note); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to add workspace alias", "workspace alias の追加に失敗しました"), err)
	}
	_, err := fmt.Fprintf(output, "Added workspace alias session_id=%s workspace=%s\n", sessionID, workspace)
	if err != nil {
		return xerrors.Errorf("failed to print added workspace alias: %w", err)
	}
	return nil
}

func (c *RootCLI) runStoreWorkspaceAliasRemove(ctx context.Context, output io.Writer, dbPath, sessionID, workspace string) error {
	if err := c.initializeWorkspaceIdentityStore(ctx, dbPath); err != nil {
		return err
	}
	if err := c.workspaceIdentity.RemoveAlias(ctx, types.SessionID(sessionID), types.Workspace(workspace)); err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to remove workspace alias", "workspace alias の削除に失敗しました"), err)
	}
	_, err := fmt.Fprintf(output, "Removed workspace alias session_id=%s workspace=%s\n", sessionID, workspace)
	if err != nil {
		return xerrors.Errorf("failed to print removed workspace alias: %w", err)
	}
	return nil
}

func (c *RootCLI) runStoreWorkspaceAliasList(ctx context.Context, output io.Writer, dbPath string, asJSON bool) error {
	if err := c.initializeWorkspaceIdentityStore(ctx, dbPath); err != nil {
		return err
	}
	report, err := c.workspaceIdentity.Report(ctx, 0)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to list workspace aliases", "workspace alias の一覧取得に失敗しました"), err)
	}
	if asJSON {
		encoder := json.NewEncoder(output)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report.Aliases); err != nil {
			return xerrors.Errorf("failed to encode workspace aliases: %w", err)
		}
		return nil
	}
	for _, alias := range report.Aliases {
		if _, err := fmt.Fprintf(output, "session_id=%s workspace=%s reviewed_at=%s reviewed_by=%s note=%q\n", alias.SessionID, alias.Workspace, alias.ReviewedAt.Format("2006-01-02T15:04:05Z07:00"), alias.ReviewedBy, alias.Note); err != nil {
			return xerrors.Errorf("failed to print workspace alias: %w", err)
		}
	}
	return nil
}
