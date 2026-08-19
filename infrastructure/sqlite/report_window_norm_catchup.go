package sqlite

import (
	"context"
	"database/sql"
	"log/slog"

	"golang.org/x/xerrors"
)

// reportWindowNormCatchUpBatchSize bounds one UPDATE of empty started_at_norm
// / observed_at_norm values. Initialize loops until the columns are full or
// the attempt cap is hit so a 16 GiB events store does not rewrite a
// store-sized table in one transaction, while sessions and usage still finish
// in a typical open.
const reportWindowNormCatchUpBatchSize = 2000
const reportWindowNormCatchUpMaxBatches = 50

type reportWindowNormCatchUpResult struct {
	SessionsUpdated int
	UsageUpdated    int
	MorePending     bool
}

func catchUpReportWindowNorm(ctx context.Context, db *sql.DB, batchSize int) (reportWindowNormCatchUpResult, error) {
	if batchSize <= 0 {
		return reportWindowNormCatchUpResult{}, xerrors.Errorf("report window norm catch-up batch size must be positive")
	}
	sessionsReady, err := databaseColumnExists(ctx, db, "sessions", "started_at_norm")
	if err != nil {
		return reportWindowNormCatchUpResult{}, err
	}
	usageReady, err := databaseColumnExists(ctx, db, "usage_observations", "observed_at_norm")
	if err != nil {
		return reportWindowNormCatchUpResult{}, err
	}
	if !sessionsReady && !usageReady {
		return reportWindowNormCatchUpResult{}, nil
	}

	var result reportWindowNormCatchUpResult
	if sessionsReady {
		updated, more, err := catchUpReportWindowNormTable(
			ctx, db, batchSize,
			`UPDATE sessions SET started_at_norm = ts_norm(started_at)
			  WHERE rowid IN (
			        SELECT rowid FROM sessions WHERE started_at_norm = '' LIMIT ?
			  )`,
		)
		result.SessionsUpdated = updated
		result.MorePending = result.MorePending || more
		if err != nil {
			return result, err
		}
	}
	if usageReady {
		updated, more, err := catchUpReportWindowNormTable(
			ctx, db, batchSize,
			`UPDATE usage_observations SET observed_at_norm = ts_norm(observed_at)
			  WHERE rowid IN (
			        SELECT rowid FROM usage_observations WHERE observed_at_norm = '' LIMIT ?
			  )`,
		)
		result.UsageUpdated = updated
		result.MorePending = result.MorePending || more
		if err != nil {
			return result, err
		}
	}
	return result, nil
}

func catchUpReportWindowNormTable(ctx context.Context, db *sql.DB, batchSize int, query string) (int, bool, error) {
	updated := 0
	for batch := 0; batch < reportWindowNormCatchUpMaxBatches; batch++ {
		result, err := db.ExecContext(ctx, query, batchSize)
		if err != nil {
			return updated, false, xerrors.Errorf("failed to stamp report window norm column: %w", err)
		}
		n, err := result.RowsAffected()
		if err != nil {
			return updated, false, xerrors.Errorf("failed to count report window norm stamps: %w", err)
		}
		updated += int(n)
		if n == 0 {
			return updated, false, nil
		}
		if n < int64(batchSize) {
			return updated, false, nil
		}
	}
	return updated, true, nil
}

func logReportWindowNormCatchUp(result reportWindowNormCatchUpResult, err error) {
	if err != nil {
		slog.Error("report window norm catch-up incomplete; retrying on next initialization",
			"sessions_updated", result.SessionsUpdated,
			"usage_updated", result.UsageUpdated,
			"more_pending", result.MorePending,
			"error", err,
		)
		return
	}
	if result.SessionsUpdated == 0 && result.UsageUpdated == 0 && !result.MorePending {
		return
	}
	if result.MorePending {
		slog.Info("report window norm catch-up paused; remaining empty norms retry on next initialization",
			"sessions_updated", result.SessionsUpdated,
			"usage_updated", result.UsageUpdated,
		)
		return
	}
	slog.Debug("report window norm catch-up completed",
		"sessions_updated", result.SessionsUpdated,
		"usage_updated", result.UsageUpdated,
	)
}
