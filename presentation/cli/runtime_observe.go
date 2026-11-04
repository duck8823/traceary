package cli

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

type observationSelectionFlags struct{ epoch, run, generation, worktree, profile, host, version, event, kind, session, turn string }

func (f observationSelectionFlags) observation() (app.SelectedObservation, error) {
	metadata, err := domain.DerivedObservationOf(domain.DerivedObservationState{Host: f.host, HostVersion: f.version, EventID: f.event, Kind: domain.ObservationKind(f.kind), NativeSessionID: f.session, NativeTurnID: f.turn})
	if err != nil {
		return app.SelectedObservation{}, domain.ErrInvalid
	}
	value, err := app.SelectedObservationOf(app.OutputSelection{Identity: domain.LaunchIdentity{Epoch: f.epoch, Run: f.run, Generation: f.generation}, Worktree: domain.WorktreeID(f.worktree), Profile: domain.ProfileID(f.profile)}, metadata)
	if err != nil {
		return app.SelectedObservation{}, xerrors.Errorf("validate observation selection: %w", err)
	}
	return value, nil
}
func explicitObservationRoot(root string) bool {
	return len(root) > 0 && len(root) <= 4096 && utf8.ValidString(root) && filepath.IsAbs(root) && filepath.Clean(root) == root && !strings.ContainsAny(root, "\x00\r\n")
}
func (c *RootCLI) newRuntimeObserveCommand(root *string) *cobra.Command {
	var flags observationSelectionFlags
	command := &cobra.Command{Use: "observe", Short: "Deliver bounded untrusted runtime metadata without reading stdin or starting a daemon", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), app.ObservationDeliveryBudget)
		defer cancel()
		if !explicitObservationRoot(*root) {
			return domain.ErrInvalid
		}
		value, err := flags.observation()
		if err != nil {
			return err
		}
		client, _ := c.runtimeDaemon.(app.SelectedObservationClient)
		outcome := app.NewObservationDelivery(client).Deliver(ctx, *root, func(context.Context) (app.SelectedObservation, error) { return value, nil })
		return writeRuntimeJSON(cmd, struct {
			Version   int                    `json:"schema_version"`
			Outcome   app.ObservationOutcome `json:"outcome"`
			Binding   string                 `json:"binding"`
			Authority string                 `json:"authority"`
		}{1, outcome, "unbound", "admission_only"})
	}}
	for _, f := range []struct {
		name  string
		value *string
	}{{"epoch", &flags.epoch}, {"run", &flags.run}, {"generation", &flags.generation}, {"worktree", &flags.worktree}, {"profile", &flags.profile}, {"host", &flags.host}, {"host-version", &flags.version}, {"event-id", &flags.event}, {"kind", &flags.kind}, {"native-session-id", &flags.session}, {"native-turn-id", &flags.turn}} {
		command.Flags().StringVar(f.value, f.name, "", "Explicit bounded correlation metadata; never native authority")
	}
	requireRuntimeFlags(command, "epoch", "run", "generation", "worktree", "profile", "host", "host-version", "event-id", "kind")
	return command
}
