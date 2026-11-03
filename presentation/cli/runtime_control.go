package cli

import (
	"encoding/hex"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"io"
	"strings"
)

type controlSelectionFlags struct{ epoch, run, generation, worktree, profile, id string }

func (f controlSelectionFlags) selection() (app.OutputSelection, error) {
	id := domain.LaunchIdentity{Epoch: f.epoch, Run: f.run, Generation: f.generation}
	if !id.Valid() || len(f.worktree) != 66 || !strings.HasPrefix(f.worktree, "w_") || f.worktree != strings.ToLower(f.worktree) {
		return app.OutputSelection{}, domain.ErrInvalid
	}
	if _, err := hex.DecodeString(f.worktree[2:]); err != nil {
		return app.OutputSelection{}, domain.ErrInvalid
	}
	profile, err := domain.ProfileIDFrom(f.profile)
	if err != nil {
		return app.OutputSelection{}, domain.ErrInvalid
	}
	if _, err = domain.ControlRequestIDFrom(f.id); err != nil {
		return app.OutputSelection{}, domain.ErrInvalid
	}
	return app.OutputSelection{Identity: id, Worktree: domain.WorktreeID(f.worktree), Profile: profile}, nil
}
func (c *RootCLI) newRuntimeControlCommand(root *string, kind string) *cobra.Command {
	var selected controlSelectionFlags
	var manual, ack bool
	var sequence string
	var rows, cols uint16
	shorts := map[string]string{"input": "Write bounded explicit manual stdin bytes once, without appending Enter", "resize": "Apply an explicit next-sequence resize to the owned PTY", "stop": "Send explicit SIGTERM to the original owned group (work loss; escaped groups are not swept)", "receipt": "Inspect safe control evidence without replay or native effects"}
	cmd := &cobra.Command{Use: kind, Short: shorts[kind], Args: func(_ *cobra.Command, args []string) error {
		if len(args) != 0 {
			return domain.ErrInvalid
		}
		return nil
	}, RunE: func(cmd *cobra.Command, _ []string) error {
		client, ok := c.runtimeClient.(app.ControlClient)
		if !ok {
			return app.ErrRuntimeUnsupported
		}
		selection, err := selected.selection()
		if err != nil {
			return err
		}
		path, err := resolveRuntimeDir(*root)
		if err != nil {
			return err
		}
		if kind == "receipt" {
			result, err := client.Receipt(cmd.Context(), path, selection, selected.id)
			if err != nil {
				return xerrors.Errorf("inspect runtime control receipt: %w", err)
			}
			return writeRuntimeJSON(cmd, controlResultView(result))
		}
		state := domain.ControlIntentState{RequestID: selected.id, Kind: domain.ControlKind(kind), ManualTerminal: manual, WorkLossAcknowledged: ack, Rows: rows, Cols: cols}
		var data []byte
		if kind == "input" {
			if !manual {
				return domain.ErrInvalid
			}
			data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), domain.MaxControlInputBytes+1))
			defer clear(data)
			if err != nil {
				return domain.ErrInvalid
			}
			if len(data) > domain.MaxControlInputBytes {
				return domain.ErrLimit
			}
			if len(data) == 0 {
				return domain.ErrInvalid
			}
			state.InputBytes = len(data)
		}
		if kind == "resize" {
			value, e := domain.ParseByteOffset(sequence)
			if e != nil {
				return domain.ErrInvalid
			}
			state.ResizeSequence = uint64(value)
		}
		intent, err := domain.ControlIntentFrom(state)
		if err != nil {
			return xerrors.Errorf("validate explicit terminal intent: %w", err)
		}
		result, err := client.Control(cmd.Context(), path, app.ControlRequest{Selection: selection, Intent: intent, Data: data})
		if !result.Receipt.Identity.Valid() {
			outcome := domain.ControlRejected
			if errors.Is(err, app.ErrControlUnknown) {
				outcome = domain.ControlUnknown
			}
			result = app.ControlResult{Selection: selection, Receipt: domain.ControlSnapshot{Identity: selection.Identity, Intent: state, Outcome: outcome}}
		}
		if writeErr := writeRuntimeJSON(cmd, controlResultView(result)); writeErr != nil {
			return writeErr
		}
		if err != nil {
			return xerrors.Errorf("explicit runtime terminal control: %w", err)
		}
		return nil
	}}
	flags := cmd.Flags()
	flags.StringVar(&selected.epoch, "epoch", "", "Current owner epoch")
	flags.StringVar(&selected.run, "run", "", "Actual owned run ID")
	flags.StringVar(&selected.generation, "generation", "", "Exact launch generation")
	flags.StringVar(&selected.worktree, "worktree", "", "Immutable registered worktree ID")
	flags.StringVar(&selected.profile, "profile", "", "Registered pinned host profile ID")
	flags.StringVar(&selected.id, "request-id", "", "Non-secret control request ID; identical IDs inspect only, never replay")
	requireRuntimeFlags(cmd, "epoch", "run", "generation", "worktree", "profile", "request-id")
	switch kind {
	case "input":
		flags.BoolVar(&manual, "manual-terminal", false, "Classify explicit operator terminal bytes, not automated review/approval; stdin only, no implicit Enter")
		requireRuntimeFlags(cmd, "manual-terminal")
	case "resize":
		flags.StringVar(&sequence, "sequence", "", "Exact next canonical decimal resize sequence")
		flags.Uint16Var(&rows, "rows", 0, "Rows (1..65535)")
		flags.Uint16Var(&cols, "cols", 0, "Columns (1..65535)")
		requireRuntimeFlags(cmd, "sequence", "rows", "cols")
	case "stop":
		flags.BoolVar(&ack, "acknowledge-work-loss", false, "Acknowledge original-group SIGTERM/work loss; escaped groups are not swept; kernel terminal hangup may affect the foreground group")
		requireRuntimeFlags(cmd, "acknowledge-work-loss")
	}
	return cmd
}
func controlResultView(result app.ControlResult) map[string]any {
	receipt := result.Receipt
	i := receipt.Intent
	s := result.Selection
	return map[string]any{"selection": map[string]string{"epoch": s.Identity.Epoch, "run": s.Identity.Run, "generation": s.Identity.Generation, "worktree": string(s.Worktree), "profile": string(s.Profile)}, "receipt": map[string]any{"request_id": i.RequestID, "kind": string(i.Kind), "manual_terminal": i.ManualTerminal, "work_loss_acknowledged": i.WorkLossAcknowledged, "input_bytes": domain.ByteOffset(i.InputBytes).String(), "resize": map[string]any{"sequence": domain.ByteOffset(i.ResizeSequence).String(), "rows": i.Rows, "cols": i.Cols}, "outcome": string(receipt.Outcome), "written_bytes": domain.ByteOffset(receipt.WrittenBytes).String()}}
}
