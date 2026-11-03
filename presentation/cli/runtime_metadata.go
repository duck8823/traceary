package cli

import (
	"context"
	"path/filepath"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newRuntimeMetadataCommand(root *string) *cobra.Command {
	group := &cobra.Command{Use: "metadata", Short: "Audit or explicitly forget persisted runtime history; requires --runtime-dir"}
	open := func(cmd *cobra.Command) (app.MetadataAudit, context.Context, context.CancelFunc, error) {
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		// Destructive metadata operations never select HOME/XDG/default roots
		// or silently normalize a different resource spelling.
		if *root == "" || !filepath.IsAbs(*root) || filepath.Clean(*root) != *root || *root == "/" {
			return nil, ctx, cancel, domain.ErrInvalid
		}
		factory, ok := c.runtimeOperators.(app.MetadataAuditFactory)
		if !ok {
			return nil, ctx, cancel, app.ErrRuntimeUnsupported
		}
		service, err := factory.OpenMetadata(ctx, *root)
		if err != nil {
			return nil, ctx, cancel, xerrors.Errorf("open explicit runtime metadata: %w", err)
		}
		if service == nil {
			return nil, ctx, cancel, app.ErrRuntimeUnsupported
		}
		return service, ctx, cancel, nil
	}
	export := &cobra.Command{Use: "export", Short: "Export bounded audit summaries to stdout, not a restore backup or liveness proof", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		service, ctx, cancel, err := open(cmd)
		defer cancel()
		if err != nil {
			return err
		}
		view, err := service.Export(ctx)
		if err != nil {
			return xerrors.Errorf("export runtime metadata: %w", err)
		}
		return writeRuntimeJSON(cmd, view)
	}}
	preview := &cobra.Command{Use: "forget-preview", Short: "Preview exact persisted record loss; never stop a daemon or erase a native conversation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		service, ctx, cancel, err := open(cmd)
		defer cancel()
		if err != nil {
			return err
		}
		view, err := service.PreviewForget(ctx)
		if err != nil {
			return xerrors.Errorf("preview runtime metadata loss: %w", err)
		}
		return writeRuntimeJSON(cmd, view)
	}}
	var digest string
	var historyLoss, recoveryLoss bool
	forget := &cobra.Command{Use: "forget", Short: "Forget ALL recorded launches/receipts only while offline; native recovery linkage will be lost", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		confirmation, err := domain.MetadataForgetConfirmationOf(digest, historyLoss, recoveryLoss)
		if err != nil {
			return xerrors.Errorf("confirm historical metadata and recovery-record loss: %w", err)
		}
		service, ctx, cancel, err := open(cmd)
		defer cancel()
		if err != nil {
			return err
		}
		result, err := service.Forget(ctx, confirmation)
		if err != nil {
			return xerrors.Errorf("forget runtime metadata; export and preview before a new decision: %w", err)
		}
		return writeRuntimeJSON(cmd, result)
	}}
	forget.Flags().StringVar(&digest, "preview-digest", "", "Exact digest from a fresh forget-preview of this root")
	forget.Flags().BoolVar(&historyLoss, "acknowledge-forget-history", false, "Acknowledge erasure of ALL persisted launch/control receipt history")
	forget.Flags().BoolVar(&recoveryLoss, "acknowledge-recovery-record-loss", false, "Acknowledge loss of recovery linkage; native conversations are NOT erased")
	requireRuntimeFlags(forget, "preview-digest", "acknowledge-forget-history", "acknowledge-recovery-record-loss")
	group.AddCommand(export, preview, forget)
	return group
}
