package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/xerrors"
)

type storeRetentionPlanInput struct {
	dbPath       string
	keepDays     int
	recoveryPath string
	outputPath   string
}

type storeRetentionExecutionInput struct {
	dbPath          string
	planPath        string
	recoveryPath    string
	confirmedPlanID string
}

func (c *RootCLI) newStoreRetentionCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "retention",
		Short: Localize("Plan and apply opt-in raw-body retention", "opt-in の本文保持ポリシーを計画・適用する"),
	}
	command.AddCommand(c.newStoreRetentionPlanCommand())
	command.AddCommand(c.newStoreRetentionApplyCommand())
	command.AddCommand(c.newStoreRetentionRestoreCommand())
	command.AddCommand(c.newStoreFileRetentionCommand())
	return command
}

func (c *RootCLI) newStoreRetentionPlanCommand() *cobra.Command {
	input := storeRetentionPlanInput{keepDays: defaultRetentionDays}
	command := &cobra.Command{
		Use:   "plan",
		Short: Localize("Write an immutable raw-body retention plan", "変更不能な本文保持計画を書き出す"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreRetentionPlan(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	command.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	command.Flags().IntVar(&input.keepDays, "keep-days", defaultRetentionDays, Localize("number of days of raw bodies to retain", "本文を保持する日数"))
	command.Flags().StringVar(&input.recoveryPath, "recovery", "", Localize("verified unencrypted store archive covering every candidate", "全候補を含む検証済み・非暗号化 store archive"))
	command.Flags().StringVar(&input.outputPath, "output", "", Localize("new plan output path", "新しい plan の出力先"))
	_ = command.MarkFlagRequired("recovery")
	_ = command.MarkFlagRequired("output")
	return command
}

func (c *RootCLI) newStoreRetentionApplyCommand() *cobra.Command {
	input := storeRetentionExecutionInput{}
	command := &cobra.Command{
		Use:   "apply",
		Short: Localize("Apply an exact reviewed raw-body retention plan", "review 済み本文保持計画を厳密に適用する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreRetentionApply(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	addStoreRetentionExecutionFlags(command, &input)
	return command
}

func (c *RootCLI) newStoreRetentionRestoreCommand() *cobra.Command {
	input := storeRetentionExecutionInput{}
	command := &cobra.Command{
		Use:   "restore",
		Short: Localize("Restore raw bodies from the reviewed recovery package", "review 済み recovery package から本文を復元する"),
		Args:  noArgsLocalized(),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return c.runStoreRetentionRestore(cmd.Context(), cmd.OutOrStdout(), input)
		},
	}
	addStoreRetentionExecutionFlags(command, &input)
	return command
}

func addStoreRetentionExecutionFlags(command *cobra.Command, input *storeRetentionExecutionInput) {
	command.Flags().StringVar(&input.dbPath, "db-path", "", dbPathFlagUsage())
	command.Flags().StringVar(&input.planPath, "plan", "", Localize("reviewed plan path", "review 済み plan path"))
	command.Flags().StringVar(&input.recoveryPath, "recovery", "", Localize("recovery package pinned by the plan", "plan が固定した recovery package"))
	command.Flags().StringVar(&input.confirmedPlanID, "confirm-plan-id", "", Localize("exact reviewed plan ID", "review 済み plan ID の完全値"))
	_ = command.MarkFlagRequired("plan")
	_ = command.MarkFlagRequired("recovery")
	_ = command.MarkFlagRequired("confirm-plan-id")
}

func (c *RootCLI) runStoreRetentionPlan(ctx context.Context, output io.Writer, input storeRetentionPlanInput) error {
	if c.rawBodyRetention == nil || c.storeManagement == nil {
		return xerrors.Errorf("%s", Localize("raw-body retention is not configured", "本文保持ポリシーが設定されていません"))
	}
	if input.keepDays <= 0 || input.keepDays > 36500 {
		return xerrors.Errorf("%s", Localize("--keep-days must be between 1 and 36500", "--keep-days は 1 から 36500 の範囲が必要です"))
	}
	if strings.TrimSpace(input.outputPath) == "" {
		return xerrors.Errorf("%s", Localize("--output is required", "--output が必要です"))
	}
	if _, err := os.Stat(input.outputPath); err == nil {
		return xerrors.Errorf("%s", Localize("plan output already exists", "plan 出力先はすでに存在します"))
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("stat plan output: %w", err)
	}
	if err := c.bindRetentionStore(input.dbPath); err != nil {
		return err
	}
	now := gcNowFunc().UTC()
	plan, err := c.rawBodyRetention.CreatePlan(ctx, now.AddDate(0, 0, -input.keepDays), input.recoveryPath, now)
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create retention plan", "保持計画を作成できませんでした"), err)
	}
	file, err := os.OpenFile(input.outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return xerrors.Errorf("create plan output: %w", err)
	}
	if _, err := file.Write(plan); err != nil {
		_ = file.Close()
		return xerrors.Errorf("write plan output: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return xerrors.Errorf("sync plan output: %w", err)
	}
	if err := file.Close(); err != nil {
		return xerrors.Errorf("close plan output: %w", err)
	}
	var header struct {
		PlanID string `json:"plan_id"`
	}
	if err := json.Unmarshal(plan, &header); err != nil {
		return xerrors.Errorf("read generated plan ID: %w", err)
	}
	var figure struct {
		CanonicalPayload struct {
			ClassResults []struct {
				Ceilings []struct {
					Current struct {
						Bytes string `json:"bytes"`
					} `json:"current"`
					Projected struct {
						Bytes string `json:"bytes"`
					} `json:"projected"`
				} `json:"ceilings"`
			} `json:"class_results"`
			Candidates []struct {
				LogicalExtent struct {
					Bytes string `json:"bytes"`
				} `json:"logical_extent"`
			} `json:"candidates"`
		} `json:"canonical_payload"`
	}
	if err := json.Unmarshal(plan, &figure); err != nil {
		return xerrors.Errorf("read retention plan reclaim figure: %w", err)
	}
	netChange := "0"
	if len(figure.CanonicalPayload.ClassResults) == 1 && len(figure.CanonicalPayload.ClassResults[0].Ceilings) == 1 {
		ceiling := figure.CanonicalPayload.ClassResults[0].Ceilings[0]
		current, err := retentionNetChange(ceiling.Current.Bytes, ceiling.Projected.Bytes)
		if err != nil {
			return xerrors.Errorf("compute retention plan reclaim figure: %w", err)
		}
		netChange = current
	}
	if _, err := fmt.Fprintf(output, "Plan: %s\nPlan ID: %s\nNet body-column change after retention markers are written (encoded bytes): %s\nCandidates: %d\n", input.outputPath, header.PlanID, netChange, len(figure.CanonicalPayload.Candidates)); err != nil {
		return xerrors.Errorf("print retention plan result: %w", err)
	}
	if zeroReclaim, ok := retentionZeroReclaimCount(figure.CanonicalPayload.Candidates, figure.CanonicalPayload.ClassResults); ok {
		if _, err := fmt.Fprintf(output, "Bodies still discarded while reclaiming essentially nothing (encoded extent at or below marker): %d\n", zeroReclaim); err != nil {
			return xerrors.Errorf("print retention plan disclosure: %w", err)
		}
	}
	return nil
}

func retentionZeroReclaimCount(candidates []struct {
	LogicalExtent struct {
		Bytes string `json:"bytes"`
	} `json:"logical_extent"`
}, classResults []struct {
	Ceilings []struct {
		Current struct {
			Bytes string `json:"bytes"`
		} `json:"current"`
		Projected struct {
			Bytes string `json:"bytes"`
		} `json:"projected"`
	} `json:"ceilings"`
}) (int, bool) {
	if len(candidates) == 0 || len(classResults) != 1 || len(classResults[0].Ceilings) != 1 {
		return 0, false
	}
	markerBytes, err := strconv.Atoi(classResults[0].Ceilings[0].Projected.Bytes)
	if err != nil || markerBytes < 0 || markerBytes%len(candidates) != 0 {
		return 0, false
	}
	markerBytes /= len(candidates)
	zeroReclaim := 0
	for _, candidate := range candidates {
		encodedBytes, err := strconv.Atoi(candidate.LogicalExtent.Bytes)
		if err != nil || encodedBytes < 0 {
			return 0, false
		}
		if encodedBytes <= markerBytes {
			zeroReclaim++
		}
	}
	return zeroReclaim, true
}

func retentionNetChange(current, projected string) (string, error) {
	currentBytes, err := strconv.Atoi(current)
	if err != nil || currentBytes < 0 {
		return "", xerrors.Errorf("invalid current retention extent")
	}
	projectedBytes, err := strconv.Atoi(projected)
	if err != nil || projectedBytes < 0 {
		return "", xerrors.Errorf("invalid projected retention extent")
	}
	return strconv.Itoa(currentBytes - projectedBytes), nil
}

func (c *RootCLI) runStoreRetentionApply(ctx context.Context, output io.Writer, input storeRetentionExecutionInput) error {
	plan, err := c.readRetentionExecutionInput(input)
	if err != nil {
		return err
	}
	result, err := c.rawBodyRetention.Apply(ctx, plan, input.recoveryPath, input.confirmedPlanID, gcNowFunc().UTC())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("retention apply failed", "保持計画の適用に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "Plan ID: %s\nCandidates: %d\nPruned: %d\nAlready pruned: %d\nCompaction: not run\n", result.PlanID, result.CandidateCount, result.PrunedCount, result.AlreadyPruned); err != nil {
		return xerrors.Errorf("print retention apply result: %w", err)
	}
	return nil
}

func (c *RootCLI) runStoreRetentionRestore(ctx context.Context, output io.Writer, input storeRetentionExecutionInput) error {
	plan, err := c.readRetentionExecutionInput(input)
	if err != nil {
		return err
	}
	result, err := c.rawBodyRetention.Restore(ctx, plan, input.recoveryPath, input.confirmedPlanID, gcNowFunc().UTC())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("retention restore failed", "保持データの復元に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "Plan ID: %s\nCandidates: %d\nRestored: %d\nAlready restored: %d\n", result.PlanID, result.CandidateCount, result.RestoredCount, result.AlreadyRestored); err != nil {
		return xerrors.Errorf("print retention restore result: %w", err)
	}
	return nil
}

func (c *RootCLI) readRetentionExecutionInput(input storeRetentionExecutionInput) ([]byte, error) {
	if c.rawBodyRetention == nil || c.storeManagement == nil {
		return nil, xerrors.Errorf("%s", Localize("raw-body retention is not configured", "本文保持ポリシーが設定されていません"))
	}
	if err := c.bindRetentionStore(input.dbPath); err != nil {
		return nil, err
	}
	plan, err := os.ReadFile(input.planPath)
	if err != nil {
		return nil, xerrors.Errorf("read retention plan: %w", err)
	}
	return plan, nil
}

func (c *RootCLI) bindRetentionStore(dbPath string) error {
	resolved, err := resolveDBPath(dbPath)
	if err != nil {
		return xerrors.Errorf("resolve retention DB path: %w", err)
	}
	c.applyDatabasePath(resolved)
	return nil
}
