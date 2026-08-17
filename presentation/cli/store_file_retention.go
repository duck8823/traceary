package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

type storeFileRetentionPlanInput struct {
	dbPath          string
	archiveRoot     string
	backupRoot      string
	archiveMaxAge   time.Duration
	archiveMaxCount int
	archiveMaxBytes int64
	backupMaxAge    time.Duration
	backupMaxCount  int
	backupMaxBytes  int64
	expiresAfter    time.Duration
	outputPath      string
}

type storeFileRetentionApplyInput struct {
	planPath        string
	confirmedPlanID string
}

func (c *RootCLI) runStoreFileRetentionPlan(ctx context.Context, output io.Writer, input storeFileRetentionPlanInput) error {
	if c.fileRetention == nil {
		return xerrors.New(Localize("file retention is not configured", "file retention が設定されていません"))
	}
	if strings.TrimSpace(input.outputPath) == "" || input.expiresAfter <= 0 {
		return xerrors.New(Localize("--output and a positive --expires-after are required", "--output と正の --expires-after が必要です"))
	}
	if _, err := os.Stat(input.outputPath); err == nil {
		return xerrors.New(Localize("plan output already exists", "plan 出力先はすでに存在します"))
	} else if !os.IsNotExist(err) {
		return xerrors.Errorf("stat plan output: %w", err)
	}
	resolvedDBPath, err := resolveDBPath(input.dbPath)
	if err != nil {
		return xerrors.Errorf("resolve file retention database: %w", err)
	}
	classes, err := fileRetentionClassRequests(input)
	if err != nil {
		return err
	}
	plan, err := c.fileRetention.CreatePlan(ctx, apptypes.FileRetentionPlanRequest{
		DatabasePath: resolvedDBPath, OutputPath: input.outputPath, ExpiresAfter: input.expiresAfter, Classes: classes,
	}, gcNowFunc().UTC())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("failed to create file retention plan", "file retention plan を作成できませんでした"), err)
	}
	file, err := os.OpenFile(input.outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return xerrors.Errorf("create file retention plan: %w", err)
	}
	if _, err := file.Write(plan); err != nil {
		_ = file.Close()
		return xerrors.Errorf("write file retention plan: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return xerrors.Errorf("sync file retention plan: %w", err)
	}
	if err := file.Close(); err != nil {
		return xerrors.Errorf("close file retention plan: %w", err)
	}
	var header apptypes.FileRetentionPlan
	if err := json.Unmarshal(plan, &header); err != nil {
		return xerrors.Errorf("read generated file retention plan ID: %w", err)
	}
	if _, err := fmt.Fprintf(output, "Plan: %s\nPlan ID: %s\n", input.outputPath, header.PlanID); err != nil {
		return xerrors.Errorf("print file retention plan result: %w", err)
	}
	return nil
}

func (c *RootCLI) runStoreFileRetentionApply(ctx context.Context, output io.Writer, input storeFileRetentionApplyInput) error {
	if c.fileRetention == nil {
		return xerrors.New(Localize("file retention is not configured", "file retention が設定されていません"))
	}
	plan, err := os.ReadFile(input.planPath)
	if err != nil {
		return xerrors.Errorf("read file retention plan: %w", err)
	}
	result, err := c.fileRetention.Apply(ctx, plan, input.confirmedPlanID, gcNowFunc().UTC())
	if err != nil {
		return xerrors.Errorf("%s: %w", Localize("file retention apply failed", "file retention apply に失敗しました"), err)
	}
	if _, err := fmt.Fprintf(output, "Plan ID: %s\nCandidates: %d\nDeleted: %d\nAlready committed: %d\nConflicted: %d\nCompaction: not run\n", result.PlanID, result.CandidateCount, result.DeletedCount, result.AlreadyCommitted, result.ConflictedCount); err != nil {
		return xerrors.Errorf("print file retention apply result: %w", err)
	}
	return nil
}

func fileRetentionClassRequests(input storeFileRetentionPlanInput) ([]apptypes.FileRetentionClassRequest, error) {
	classes := make([]apptypes.FileRetentionClassRequest, 0, 2)
	appendClass := func(class, root string, maxAge time.Duration, maxCount int, maxBytes int64) error {
		if strings.TrimSpace(root) == "" {
			if maxAge != 0 || maxCount >= 0 || maxBytes >= 0 {
				return xerrors.Errorf("--%s-root is required when a %s ceiling is configured", class, class)
			}
			return nil
		}
		if maxAge < 0 || maxCount < -1 || maxBytes < -1 {
			return xerrors.Errorf("%s capacity ceilings must not be negative", class)
		}
		budget := apptypes.FileRetentionBudgetInput{}
		if maxAge > 0 {
			value := maxAge
			budget.MaxAge = &value
		}
		if maxCount >= 0 {
			value := maxCount
			budget.MaxCount = &value
		}
		if maxBytes >= 0 {
			value := maxBytes
			budget.MaxAllocatedBytes = &value
		}
		if budget.MaxAge == nil && budget.MaxCount == nil && budget.MaxAllocatedBytes == nil {
			return xerrors.Errorf("at least one %s capacity ceiling is required", class)
		}
		resolvedRoot, err := resolveRequiredAbsolutePath(root)
		if err != nil {
			return err
		}
		classes = append(classes, apptypes.FileRetentionClassRequest{Class: class, Root: resolvedRoot, Budget: budget})
		return nil
	}
	if err := appendClass("archive", input.archiveRoot, input.archiveMaxAge, input.archiveMaxCount, input.archiveMaxBytes); err != nil {
		return nil, err
	}
	if err := appendClass("backup", input.backupRoot, input.backupMaxAge, input.backupMaxCount, input.backupMaxBytes); err != nil {
		return nil, err
	}
	if len(classes) == 0 {
		return nil, xerrors.New(Localize("at least one archive or backup root is required", "archive または backup root が1つ以上必要です"))
	}
	return classes, nil
}
