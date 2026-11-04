package cli

import (
	"errors"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

func (c *RootCLI) newRuntimeResumeCommand(root *string) *cobra.Command {
	var selected controlSelectionFlags
	var epoch string
	var ack bool
	cmd := &cobra.Command{Use: "resume", Short: "Explicitly resume the same managed AI conversation once; never replay pending input", Long: "Resume only a qualified native CLI conversation after the old Traceary-managed AI exited. External CLI actors and all-descendant inactivity are outside the guarantee. Never sends pending input, tools or approvals.", Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return domain.ErrInvalid
		}
		return nil
	}, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ok := c.runtimeClient.(app.ResumeClient)
		if !ok {
			return app.ErrRuntimeUnsupported
		}
		previous, err := selected.selection()
		if err != nil {
			return err
		}
		request := app.ResumeRequest{Epoch: epoch, Selection: previous, RequestID: selected.id, WorkLossAcknowledged: ack}
		if !request.Valid() {
			return domain.ErrInvalid
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		result, err := client.Resume(cmd.Context(), path, request)
		outcome := "confirmed"
		if err != nil {
			outcome = "rejected"
			if errors.Is(err, app.ErrLaunchUnknown) {
				outcome = "unknown"
			}
		}
		phase := result.Phase
		if phase == "" && outcome == "unknown" {
			phase = domain.ResumeUnknown
		}
		view := map[string]any{"request_id": request.RequestID, "epoch": request.Epoch, "previous_selection": runtimeSelectionView(previous), "recorded_phase": phase, "actual_owner_handle": result.Owned, "outcome": outcome, "external_actors_coordinated": false, "descendant_inactivity_proven": false, "pending_input_replay": "never_requested"}
		if result.Selection.Identity.Valid() {
			view["selection"] = runtimeSelectionView(result.Selection)
		}
		if writeErr := writeRuntimeJSON(cmd, view); writeErr != nil {
			if err != nil {
				return errors.Join(err, writeErr)
			}
			return errors.Join(app.ErrLaunchUnknown, writeErr)
		}
		if err != nil {
			return xerrors.Errorf("explicit managed native resume: %w", err)
		}
		return nil
	}}
	f := cmd.Flags()
	f.StringVar(&epoch, "epoch", "", "Current daemon epoch")
	f.StringVar(&selected.epoch, "previous-epoch", "", "Exact original managed AI epoch, including a previous daemon epoch")
	f.StringVar(&selected.run, "run", "", "Original managed AI run ID")
	f.StringVar(&selected.generation, "generation", "", "Exact original launch generation")
	f.StringVar(&selected.worktree, "worktree", "", "Original immutable registered worktree ID")
	f.StringVar(&selected.profile, "profile", "", "Original pinned host profile ID")
	f.StringVar(&selected.id, "request-id", "", "Explicit non-secret request ID; reuse only to inspect the same outcome")
	f.BoolVar(&ack, "acknowledge-work-loss", false, "Acknowledge uncertain interrupted effects; only the managed old AI exit is guaranteed")
	requireRuntimeFlags(cmd, "epoch", "previous-epoch", "run", "generation", "worktree", "profile", "request-id", "acknowledge-work-loss")
	return cmd
}
func runtimeSelectionView(s app.OutputSelection) map[string]string {
	return map[string]string{"epoch": s.Identity.Epoch, "run": s.Identity.Run, "generation": s.Identity.Generation, "worktree": string(s.Worktree), "profile": string(s.Profile)}
}
