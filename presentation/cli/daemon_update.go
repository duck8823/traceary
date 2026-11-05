package cli

import (
	"context"
	"errors"
	"time"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

const updateAuthority = "current_owner_metadata_only_not_installation"

func updateTargetView(t domain.UpdateTarget) map[string]any {
	return map[string]any{"version": t.Version, "protocol": t.Protocol}
}
func updateStatusView(s app.UpdateStatus) map[string]any {
	v := map[string]any{"authority": updateAuthority, "epoch": s.Epoch, "quiescent": s.Quiescent, "blockers": s.Blockers, "overdue_24h": s.Overdue24h, "notice_delivery": "not_proven"}
	if state, ok := s.Deferral.Value(); ok {
		v["update_deferral"] = map[string]any{"version": state.Target.Version, "protocol": state.Target.Protocol, "started": state.Started.UTC().Format(time.RFC3339Nano), "notified_24h": state.Notified24h}
	}
	return v
}
func parseUpdateTarget(version, protocol string) (domain.UpdateTarget, error) {
	p, err := domain.ParseByteOffset(protocol)
	if err != nil || uint64(p) > 65535 {
		return domain.UpdateTarget{}, domain.ErrInvalid
	}
	t := domain.UpdateTarget{Version: version, Protocol: int(p)}
	if !t.Valid() {
		return domain.UpdateTarget{}, domain.ErrInvalid
	}
	return t, nil
}
func (c *RootCLI) newDaemonUpdateCommand(root *string) *cobra.Command {
	group := &cobra.Command{Use: "update", Short: "Inspect/defer daemon updates or explicitly hand off an empty owner; never install software"}
	for _, kind := range []string{"status", "attempt", "handoff"} {
		var epoch, version, protocol string
		var selected shutdownBindingFlags
		var ack bool
		command := &cobra.Command{Use: kind, Short: map[string]string{"status": "Read non-consuming update eligibility and continuous deferral", "attempt": "Record an update attempt notice without stopping work or installing software", "handoff": "Explicitly exit only the exact empty owner; never close live work"}[kind], Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
			client, ok := c.runtimeDaemon.(app.UpdateClient)
			if !ok {
				return app.ErrRuntimeUnsupported
			}
			var target domain.UpdateTarget
			var binding domain.ShutdownBindingState
			var err error
			if kind == "handoff" {
				binding, err = selected.binding()
				if err != nil {
					return err
				}
				epoch = binding.Epoch
			}
			if !(domain.LaunchIdentity{Epoch: epoch, Run: epoch, Generation: epoch}).Valid() {
				return domain.ErrInvalid
			}
			if kind != "status" {
				target, err = parseUpdateTarget(version, protocol)
				if err != nil {
					return err
				}
			}
			request := app.UpdateHandoffRequest{Target: target, Binding: binding, DaemonExitAcknowledged: ack}
			if kind == "handoff" && !request.Valid() {
				return domain.ErrInvalid
			}
			path, err := resolveRuntimeDir(*root)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 8*time.Second)
			defer cancel()
			switch kind {
			case "status":
				status, e := client.UpdateStatus(ctx, path, epoch)
				if e != nil {
					return xerrors.Errorf("read daemon update status: %w", e)
				}
				if !status.Valid() || status.Epoch != epoch {
					return app.ErrRuntimeProtocol
				}
				return writeRuntimeJSON(cmd, updateStatusView(status))
			case "attempt":
				result, e := client.AttemptUpdate(ctx, path, app.UpdateAttemptRequest{Epoch: epoch, Target: target})
				view := map[string]any{"authority": updateAuthority, "target": updateTargetView(target), "outcome": "rejected", "notice_delivery": "not_proven"}
				valid := result.Valid() && result.Target == target && result.Status.Epoch == epoch
				_, preview := result.Preview.Value()
				if e == nil && (!valid || preview != result.Status.Quiescent) {
					e = errors.Join(app.ErrUpdateUnknown, app.ErrRuntimeProtocol)
				}
				if valid {
					view["status"] = updateStatusView(result.Status)
					view["notice"] = result.Notice
					if e == nil {
						view["outcome"] = "deferred"
						if b, found := result.Preview.Value(); found {
							view["outcome"] = "quiescent_snapshot_not_permission"
							view["preview"] = shutdownBindingView(b)
						}
					}
				}
				if errors.Is(e, app.ErrUpdateUnknown) {
					view["outcome"] = "unknown"
				}
				if err = writeRuntimeJSON(cmd, view); err != nil {
					return err
				}
				if e != nil {
					return xerrors.Errorf("attempt daemon update: %w", e)
				}
				return nil
			case "handoff":
				result, e := client.HandoffUpdate(ctx, path, request)
				if e == nil && (result.Binding != binding || result.Outcome != domain.ShutdownDraining || result.ClosedTerminals != 0) {
					e = errors.Join(app.ErrShutdownUnknown, app.ErrRuntimeProtocol)
				}
				if e != nil {
					outcome := domain.ShutdownRejected
					if errors.Is(e, app.ErrShutdownUnknown) {
						outcome = domain.ShutdownUnknown
					}
					result = app.ShutdownResult{Binding: binding, Outcome: outcome}
				}
				view := shutdownResultView(result)
				view["authority"] = updateAuthority
				view["target"] = updateTargetView(target)
				if err = writeRuntimeJSON(cmd, view); err != nil {
					return err
				}
				if e != nil {
					return xerrors.Errorf("hand off empty daemon update: %w", e)
				}
				return nil
			default:
				return app.ErrRuntimeUnsupported
			}
		}}
		if kind == "handoff" {
			bindShutdownFlags(command, &selected)
			command.Flags().BoolVar(&ack, "acknowledge-daemon-exit", false, "Explicitly consent to this exact empty daemon's exit, not live work loss or update installation")
			requireRuntimeFlags(command, "acknowledge-daemon-exit")
		} else {
			command.Flags().StringVar(&epoch, "epoch", "", "Current daemon epoch")
			requireRuntimeFlags(command, "epoch")
		}
		if kind != "status" {
			command.Flags().StringVar(&version, "target-version", "", "Safe target version metadata, never an executable or URL")
			command.Flags().StringVar(&protocol, "target-protocol", "", "Canonical target protocol number (metadata only)")
			requireRuntimeFlags(command, "target-version", "target-protocol")
		}
		group.AddCommand(command)
	}
	return group
}
