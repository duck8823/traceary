package cli

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"io"
)

func (c *RootCLI) newRuntimeAttachCommand(root *string) *cobra.Command {
	var epoch, run, generation, worktree, profile, cursor string
	cmd := &cobra.Command{Use: "attach", Short: "Stream bounded owned PTY output as JSON/base64 without interpreting terminal escapes", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ok := c.runtimeClient.(app.OutputClient)
		if !ok {
			return app.ErrRuntimeUnsupported
		}
		offset, err := domain.ParseByteOffset(cursor)
		if err != nil {
			return xerrors.Errorf("parse output cursor: %w", err)
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		selection := app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: epoch, Run: run, Generation: generation}, Worktree: domain.WorktreeID(worktree), Profile: domain.ProfileID(profile)}
		stream, err := client.Attach(cmd.Context(), path, selection, offset)
		if err != nil {
			return xerrors.Errorf("attach owned runtime output: %w", err)
		}
		defer func() { _ = stream.Close() }()
		for {
			frame, err := stream.Next(cmd.Context())
			if err != nil {
				if err == io.EOF {
					return nil
				}
				return xerrors.Errorf("read owned runtime output: %w", err)
			}
			if err = writeRuntimeJSON(cmd, outputView(frame)); err != nil {
				return err
			}
			if frame.Kind == "end" || frame.Gap != nil && frame.Gap.Reason == "slow_consumer" {
				return nil
			}
		}
	}}
	cmd.Flags().StringVar(&epoch, "epoch", "", "Current owner epoch")
	cmd.Flags().StringVar(&run, "run", "", "Owned run ID")
	cmd.Flags().StringVar(&generation, "generation", "", "Exact launch generation")
	cmd.Flags().StringVar(&worktree, "worktree", "", "Immutable worktree ID")
	cmd.Flags().StringVar(&profile, "profile", "", "Registered profile ID")
	cmd.Flags().StringVar(&cursor, "cursor", "0", "Canonical decimal byte cursor (not a character offset)")
	requireRuntimeFlags(cmd, "epoch", "run", "generation", "worktree", "profile")
	return cmd
}

// outputView deliberately emits no raw bytes or numeric uint64 counters. JSON byte
// slices are base64, so terminal controls, OSC links and clipboard requests stay inert.
func outputView(f app.OutputFrame) map[string]any {
	info := map[string]any{
		"selection": map[string]string{"epoch": f.Info.Selection.Identity.Epoch, "run": f.Info.Selection.Identity.Run, "generation": f.Info.Selection.Identity.Generation, "worktree": string(f.Info.Selection.Worktree), "profile": string(f.Info.Selection.Profile)},
		"base":      f.Info.Base.String(), "head": f.Info.Head.String(), "ended": f.Info.Ended,
		"resize": map[string]any{"sequence": domain.ByteOffset(f.Info.Resize.Sequence).String(), "rows": f.Info.Resize.Rows, "cols": f.Info.Resize.Cols},
	}
	if f.Info.Reason != "" {
		info["reason"] = string(f.Info.Reason)
	}
	view := map[string]any{"kind": f.Kind, "info": info}
	if f.Kind == "data" {
		view["start"] = f.Start.String()
		view["end"] = f.End.String()
		view["data"] = f.Data
	}
	if f.Gap != nil {
		view["gap"] = map[string]any{"from": f.Gap.From.String(), "to": f.Gap.To.String(), "reason": f.Gap.Reason, "delivery_unknown": f.Gap.DeliveryUnknown}
	}
	return view
}
