package cli

import (
	"context"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func historyCLITime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.IsZero() || t.Year() < 1 || t.Year() > 9999 || t.UTC().Format(time.RFC3339Nano) != s {
		return time.Time{}, domain.ErrInvalid
	}
	return t, nil
}
func historyAnchorView(a domain.HistoryAnchor) any {
	if a.IsZero() {
		return nil
	}
	return map[string]string{"at": a.At.Format(time.RFC3339Nano), "id": a.ID}
}
func (c *RootCLI) newRuntimeHistoryCommand(root *string) *cobra.Command {
	group := &cobra.Command{Use: "history", Short: "Read bounded committed history; never live agent state or a durable commit cursor"}
	var epoch, worktree, from, to, beforeAt, beforeID, witness string
	var limit int
	page := &cobra.Command{Use: "page", Short: "Read one registered body-free page from an existing daemon; rewalk the view after gaps", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ok := c.runtimeClient.(app.HistoryClient)
		if !ok {
			return app.ErrRuntimeUnsupported
		}
		f, err := historyCLITime(from)
		if err != nil {
			return err
		}
		t, err := historyCLITime(to)
		if err != nil {
			return err
		}
		before := domain.HistoryAnchor{}
		if beforeAt != "" || beforeID != "" || witness != "" {
			if beforeAt == "" || beforeID == "" || witness == "" {
				return domain.ErrInvalid
			}
			at, err := historyCLITime(beforeAt)
			if err != nil {
				return err
			}
			before = domain.HistoryAnchor{At: at, ID: beforeID}
		}
		selection, err := domain.HistorySelectionOf(f, t, limit, before)
		if err != nil {
			return xerrors.Errorf("history view: %w", err)
		}
		request := app.RemoteHistoryRequest{Epoch: epoch, Request: app.HistoryRequest{Worktree: domain.WorktreeID(worktree), Selection: selection, ExpectedWitness: witness}}
		if !request.Valid() {
			return domain.ErrInvalid
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		result, err := client.HistoryPage(ctx, path, request)
		if err != nil {
			return xerrors.Errorf("read runtime history: %w", err)
		}
		if result.Epoch != epoch || result.Page.Source().Worktree().ID() != request.Request.Worktree || result.Page.Selection() != selection || !result.Hint.Valid() {
			return app.ErrRuntimeProtocol
		}
		if witness != "" && result.Page.Source().Witness() != witness {
			return app.ErrRuntimeProtocol
		}
		events := []map[string]any{}
		for _, e := range result.Page.Events() {
			s := e.Snapshot()
			events = append(events, map[string]any{"id": s.ID, "kind": s.Kind, "client": s.Client, "agent": s.Agent, "session_id": s.SessionID, "workspace": s.Workspace, "source_hook": s.SourceHook, "created_at": s.CreatedAt.Format(time.RFC3339Nano)})
		}
		return writeRuntimeJSON(cmd, map[string]any{"epoch": result.Epoch, "worktree": worktree, "from": from, "to": to, "limit": limit, "events": events, "more": result.Page.More(), "next": historyAnchorView(result.Page.Next()), "source_witness": result.Page.Source().Witness(), "store_identity_changed": result.Page.Source().StoreIdentityChanged(), "native_target_revalidated": false, "hint_revision": domain.ByteOffset(result.Hint.Revision).String(), "hint_overflow": result.Hint.Overflow, "authority": "committed_page_not_commit_cursor", "consistency": "single_page_only"})
	}}
	flags := page.Flags()
	flags.StringVar(&epoch, "epoch", "", "Original daemon epoch")
	flags.StringVar(&worktree, "worktree", "", "Registered worktree ID")
	flags.StringVar(&from, "from", "", "Inclusive canonical UTC RFC3339Nano timestamp")
	flags.StringVar(&to, "to", "", "Exclusive canonical UTC RFC3339Nano timestamp")
	flags.IntVar(&limit, "limit", 64, "Requested page size (1..64)")
	flags.StringVar(&beforeAt, "before-at", "", "Previous last-retained row UTC timestamp")
	flags.StringVar(&beforeID, "before-id", "", "Previous last-retained row event ID")
	flags.StringVar(&witness, "source-witness", "", "Previous page physical source witness (required with before flags)")
	requireRuntimeFlags(page, "epoch", "worktree", "from", "to")
	group.AddCommand(page)
	return group
}
