package cli

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

type payloadRehearsalFlags struct {
	target, live, backup                  string
	batchRows                             int
	storedBytes, decodedBytes, scrubBytes int64
	wall, lock, scrubTime                 time.Duration
}

func (f *payloadRehearsalFlags) config() apptypes.PayloadRehearsalConfig {
	return apptypes.PayloadRehearsalConfig{TargetPath: f.target, LivePath: f.live, BackupPath: f.backup, BatchRows: f.batchRows, StoredByteLimit: f.storedBytes, DecodedByteLimit: f.decodedBytes, WallTimeLimit: f.wall, LockTimeLimit: f.lock, ScrubByteLimit: f.scrubBytes, ScrubTimeLimit: f.scrubTime}
}
func bindPayloadRehearsalFlags(cmd *cobra.Command, f *payloadRehearsalFlags, backup bool) {
	cmd.Flags().StringVar(&f.target, "target", "", "explicit copied SQLite target")
	cmd.Flags().StringVar(&f.live, "live-db", "", "configured live SQLite store used only for identity safety checks")
	if backup {
		cmd.Flags().StringVar(&f.backup, "backup", "", "physical rollback artifact")
	}
	cmd.Flags().IntVar(&f.batchRows, "batch-rows", 256, "maximum rows per batch")
	cmd.Flags().Int64Var(&f.storedBytes, "stored-byte-limit", 8<<20, "maximum stored bytes selected per batch")
	cmd.Flags().Int64Var(&f.decodedBytes, "decoded-byte-limit", 16<<20, "maximum decoded bytes selected per batch")
	cmd.Flags().DurationVar(&f.wall, "wall-time-limit", 30*time.Minute, "maximum run duration")
	cmd.Flags().DurationVar(&f.lock, "lock-time-limit", time.Second, "maximum write lock duration")
	cmd.Flags().Int64Var(&f.scrubBytes, "scrub-byte-limit", 1<<30, "maximum stored bytes scrubbed")
	cmd.Flags().DurationVar(&f.scrubTime, "scrub-time-limit", 30*time.Minute, "maximum scrub duration")
	_ = cmd.MarkFlagRequired("target")
	_ = cmd.MarkFlagRequired("live-db")
	if backup {
		_ = cmd.MarkFlagRequired("backup")
	}
}

func (c *RootCLI) newStorePayloadRehearsalCommand() *cobra.Command {
	group := &cobra.Command{Use: "payload-rehearsal", Short: "Rehearse payload compression on an independent copied store"}
	for _, name := range []string{"preview", "run", "resume", "scrub", "rollback"} {
		name := name
		f := &payloadRehearsalFlags{}
		cmd := &cobra.Command{Use: name, Args: noArgsLocalized(), RunE: func(cmd *cobra.Command, _ []string) error { return c.runPayloadRehearsal(cmd, name, f.config()) }}
		bindPayloadRehearsalFlags(cmd, f, name != "preview" && name != "scrub")
		group.AddCommand(cmd)
	}
	return group
}

func (c *RootCLI) runPayloadRehearsal(cmd *cobra.Command, operation string, config apptypes.PayloadRehearsalConfig) error {
	if c.payloadRehearsal == nil {
		return errors.New("payload rehearsal is not configured")
	}
	var result apptypes.PayloadRehearsalMetrics
	var err error
	switch operation {
	case "preview":
		result, err = c.payloadRehearsal.Preview(cmd.Context(), config)
	case "run":
		result, err = c.payloadRehearsal.Run(cmd.Context(), config)
	case "resume":
		result, err = c.payloadRehearsal.Resume(cmd.Context(), config)
	case "scrub":
		result, err = c.payloadRehearsal.Scrub(cmd.Context(), config)
	case "rollback":
		result, err = c.payloadRehearsal.Rollback(cmd.Context(), config)
	default:
		return errors.New("unsupported rehearsal operation")
	}
	if err != nil {
		return xerrors.Errorf("payload rehearsal %s failed: %w", operation, err)
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return xerrors.Errorf("encode payload rehearsal result: %w", err)
	}
	return nil
}
