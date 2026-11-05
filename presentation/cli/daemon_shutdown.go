package cli

import (
	"context"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"math"
	"time"
)

type shutdownBindingFlags struct{ epoch, id, revision, digest, total, pendingLaunches, pendingControls, pendingReconciliations string }

func (f shutdownBindingFlags) binding() (domain.ShutdownBindingState, error) {
	raw := []string{f.revision, f.total, f.pendingLaunches, f.pendingControls, f.pendingReconciliations}
	limits := []uint64{math.MaxUint64, domain.MaxShutdownPreviewRuns, domain.MaxActiveRuns, domain.MaxControlCoordinators, domain.MaxActiveRuns}
	values := make([]uint64, len(raw))
	for i, s := range raw {
		n, err := domain.ParseByteOffset(s)
		if err != nil || uint64(n) > limits[i] {
			return domain.ShutdownBindingState{}, domain.ErrInvalid
		}
		values[i] = uint64(n)
	}
	state := domain.ShutdownBindingState{Epoch: f.epoch, PreviewID: f.id, Revision: values[0], RosterDigest: f.digest, TotalRuns: int(values[1]), PendingLaunches: int(values[2]), PendingControls: int(values[3]), PendingReconciliations: int(values[4])}
	binding, err := domain.ShutdownBindingFrom(state)
	if err != nil {
		return domain.ShutdownBindingState{}, domain.ErrInvalid
	}
	return binding.Snapshot(), nil
}
func bindShutdownFlags(cmd *cobra.Command, f *shutdownBindingFlags) {
	flags := cmd.Flags()
	flags.StringVar(&f.epoch, "epoch", "", "Original daemon epoch")
	flags.StringVar(&f.id, "preview-id", "", "Original complete preview ID")
	flags.StringVar(&f.revision, "revision", "", "Original canonical decimal preview revision")
	flags.StringVar(&f.digest, "roster-digest", "", "Original complete roster correlation digest")
	flags.StringVar(&f.total, "total-runs", "", "Original total preview rows, including historical publication uncertainty")
	flags.StringVar(&f.pendingLaunches, "pending-launches", "", "Original pending launch count")
	flags.StringVar(&f.pendingControls, "pending-controls", "", "Original pending control count")
	flags.StringVar(&f.pendingReconciliations, "pending-reconciliations", "", "Original pending exit publication count")
	requireRuntimeFlags(cmd, "epoch", "preview-id", "revision", "roster-digest", "total-runs", "pending-launches", "pending-controls", "pending-reconciliations")
}
func shutdownBindingView(b domain.ShutdownBindingState) map[string]any {
	return map[string]any{"epoch": b.Epoch, "preview_id": b.PreviewID, "revision": domain.ByteOffset(b.Revision).String(), "roster_digest": b.RosterDigest, "total_runs": b.TotalRuns, "pending_launches": b.PendingLaunches, "pending_controls": b.PendingControls, "pending_reconciliations": b.PendingReconciliations}
}
func shutdownResultView(r app.ShutdownResult) map[string]any {
	return map[string]any{"binding": shutdownBindingView(r.Binding), "outcome": string(r.Outcome), "closed_terminals": r.ClosedTerminals}
}
func shutdownPreviewRunView(r app.ShutdownPreviewRun) map[string]any {
	return map[string]any{"selection": map[string]string{"epoch": r.Selection.Identity.Epoch, "run": r.Selection.Identity.Run, "generation": r.Selection.Identity.Generation, "worktree": string(r.Selection.Worktree), "profile": string(r.Selection.Profile)}, "state": string(r.State), "publication_uncertain": r.PublicationUncertain}
}
func (c *RootCLI) newDaemonShutdownCommand(root *string) *cobra.Command {
	group := &cobra.Command{Use: "shutdown", Short: "Preview, cancel or explicitly confirm PTY master loss; never a window detach"}
	noArgs := func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return domain.ErrInvalid
		}
		return nil
	}
	var epoch string
	preview := &cobra.Command{Use: "preview", Short: "Print every epoch-bound affected run and PTY/SIGHUP/work-loss risks; no native effects", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ok := c.runtimeDaemon.(app.LifecycleClient)
		if !ok {
			return app.ErrRuntimeUnsupported
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		plan, err := app.ReadShutdownPlan(ctx, client, path, epoch)
		if err != nil {
			return xerrors.Errorf("read complete daemon shutdown plan: %w", err)
		}
		rows := make([]map[string]any, 0, len(plan.Runs))
		for _, run := range plan.Runs {
			rows = append(rows, shutdownPreviewRunView(run))
		}
		return writeRuntimeJSON(cmd, map[string]any{"binding": shutdownBindingView(plan.Binding), "runs": rows, "risks": []string{"Closing PTY masters can cause SIGHUP, controlling-terminal loss, agent exit and work loss.", "Master closure is not proof of child/descendant death. HUP-ignoring children retain daemon ownership; escaped descendants are not swept.", "No automatic session resumption, input replay or force kill. Cancel before confirming; confirmation cannot restore closed terminals."}})
	}}
	preview.Flags().StringVar(&epoch, "epoch", "", "Current daemon epoch from daemon status --json")
	requireRuntimeFlags(preview, "epoch")
	group.AddCommand(preview)
	history := &cobra.Command{Use: "history", Short: "Read existing private lifecycle evidence offline; never adopt/resume a process", Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
		defer cancel()
		services, err := c.operator(ctx, path, false)
		if err != nil {
			return err
		}
		if services.Lifecycle == nil {
			return app.ErrRuntimeUnsupported
		}
		snapshot, err := services.Lifecycle.ReadLifecycle(ctx)
		if err != nil {
			return xerrors.Errorf("read daemon lifecycle history: %w", err)
		}
		interrupted := make([]map[string]any, 0, len(snapshot.Interrupted))
		for _, record := range snapshot.Interrupted {
			state := record.Snapshot()
			selection := state.Binding
			row := shutdownPreviewRunView(app.ShutdownPreviewRun{Selection: app.OutputSelection{Identity: selection.Identity, Worktree: selection.Worktree, Profile: selection.Profile}})
			interrupted = append(interrupted, map[string]any{"selection": row["selection"], "status": record.Status(), "pending_controls": state.PendingControls})
		}
		decisions := make([]map[string]any, 0, len(snapshot.Shutdowns))
		for _, result := range snapshot.Shutdowns {
			decisions = append(decisions, shutdownResultView(result))
		}
		view := map[string]any{"epoch": snapshot.Epoch, "previous_epoch": snapshot.PreviousEpoch, "clean": snapshot.Clean, "previous_clean": snapshot.PreviousClean, "interrupted": interrupted, "decisions": decisions, "autostart_opt_in": snapshot.AutoStartOptIn, "authority": "history_only_not_resume_permission"}
		if deferral, found := snapshot.UpdateDeferral.Value(); found {
			view["update_deferral"] = map[string]any{"version": deferral.Target.Version, "protocol": deferral.Target.Protocol, "started": deferral.Started.UTC().Format(time.RFC3339Nano), "notified_24h": deferral.Notified24h}
		}
		return writeRuntimeJSON(cmd, view)
	}}
	group.AddCommand(history)
	for _, kind := range []string{"cancel", "confirm", "receipt"} {
		var selected shutdownBindingFlags
		var ack bool
		cmd := &cobra.Command{Use: kind, Short: map[string]string{"cancel": "Discard the exact original preview before confirmation; no terminal effects", "confirm": "Acknowledge PTY/SIGHUP/work loss once; unknown is never automatically replayed", "receipt": "Inspect the original safe decision without replay"}[kind], Args: noArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			client, ok := c.runtimeDaemon.(app.LifecycleClient)
			if !ok {
				return app.ErrRuntimeUnsupported
			}
			binding, err := selected.binding()
			if err != nil {
				return err
			}
			if kind == "confirm" && !ack {
				return domain.ErrInvalid
			}
			path, err := resolveRuntimeDir(*root)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
			defer cancel()
			switch kind {
			case "cancel":
				err = client.CancelShutdown(ctx, path, binding)
				if err != nil {
					return xerrors.Errorf("cancel daemon shutdown preview: %w", err)
				}
				return writeRuntimeJSON(cmd, map[string]any{"binding": shutdownBindingView(binding), "outcome": "cancelled"})
			case "receipt":
				result, e := client.ShutdownReceipt(ctx, path, binding)
				if e != nil {
					return xerrors.Errorf("read daemon shutdown receipt: %w", e)
				}
				if result.Binding != binding {
					return app.ErrRuntimeProtocol
				}
				return writeRuntimeJSON(cmd, shutdownResultView(result))
			case "confirm":
				result, e := client.ConfirmShutdown(ctx, path, app.ShutdownConfirmation{Binding: binding, WorkLossAcknowledged: true})
				if e != nil {
					outcome := domain.ShutdownRejected
					if errors.Is(e, app.ErrShutdownUnknown) {
						outcome = domain.ShutdownUnknown
					}
					if result.Binding != binding || result.ClosedTerminals < 0 || result.ClosedTerminals > binding.TotalRuns {
						result = app.ShutdownResult{Binding: binding}
					}
					result.Outcome = outcome
				} else if result.Binding != binding || result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != binding.TotalRuns {
					result = app.ShutdownResult{Binding: binding, Outcome: domain.ShutdownUnknown}
					e = errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
				}
				if writeErr := writeRuntimeJSON(cmd, shutdownResultView(result)); writeErr != nil {
					return writeErr
				}
				if e != nil {
					return xerrors.Errorf("confirm daemon shutdown: %w", e)
				}
				return nil
			default:
				return app.ErrRuntimeUnsupported
			}
		}}
		bindShutdownFlags(cmd, &selected)
		if kind == "confirm" {
			cmd.Flags().BoolVar(&ack, "acknowledge-work-loss", false, "Explicitly acknowledge PTY close, SIGHUP, terminal loss and possible work loss")
			requireRuntimeFlags(cmd, "acknowledge-work-loss")
		}
		group.AddCommand(cmd)
	}
	return group
}
