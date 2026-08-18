//nolint:wrapcheck // CLI helpers preserve typed usecase errors for cobra RunE.
package cli

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
)

func (c *RootCLI) runStoreSearchProjectionStart(ctx context.Context, output io.Writer, budget apptypes.SearchProjectionBudget) error {
	if c.searchProjection == nil {
		return xerrors.New("search projection usecase is not configured")
	}
	got, err := c.searchProjection.StartGeneration(ctx, budget, time.Now())
	if err != nil {
		return err
	}
	return encodeProjectionRebuildGeneration(output, got)
}

func (c *RootCLI) runStoreSearchProjectionRebuild(ctx context.Context, output io.Writer, budget apptypes.SearchProjectionBudget) error {
	if c.searchProjection == nil {
		return xerrors.New("search projection usecase is not configured")
	}
	status, statusErr := c.searchProjection.ControlStatus(ctx)
	if statusErr != nil {
		return statusErr
	}
	inFlight := status.State == "rebuilding" || (status.State == "drifted" && status.Phase == "cleanup")
	if inFlight && status.ConfigHash == budget.ConfigHash() {
		return c.runStoreSearchProjectionResumeUntil(ctx, output, budget, defaultProjectionRunOptions())
	}
	if inFlight {
		if _, abandonErr := c.searchProjection.Abandon(ctx, time.Now()); abandonErr != nil {
			return abandonErr
		}
	}
	return c.runStoreSearchProjectionStart(ctx, output, budget)
}

func (c *RootCLI) runStoreSearchProjectionResumeUntil(ctx context.Context, output io.Writer, budget apptypes.SearchProjectionBudget, opts apptypes.SearchProjectionRunOptions) error {
	if c.searchProjection == nil {
		return xerrors.New("search projection usecase is not configured")
	}
	got, err := c.searchProjection.ResumeUntil(ctx, budget, opts, time.Now())
	if err != nil {
		return err
	}
	return encodeProjectionRebuildRun(output, got)
}

func (c *RootCLI) runStoreSearchProjectionAbort(ctx context.Context, output io.Writer) error {
	if c.searchProjection == nil {
		return xerrors.New("search projection usecase is not configured")
	}
	got, err := c.searchProjection.Abandon(ctx, time.Now())
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(got)
}

func (c *RootCLI) runStoreSearchProjectionInspect(ctx context.Context, output io.Writer) error {
	if c.searchProjection == nil {
		return xerrors.New("search projection usecase is not configured")
	}
	status, err := c.searchProjection.Inspect(ctx)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(status)
}

func defaultProjectionRunOptions() apptypes.SearchProjectionRunOptions {
	return apptypes.SearchProjectionRunOptions{MaxBatches: 100, TotalWallTime: 10 * time.Minute}
}

type projectionRebuildGenerationJSON struct {
	ResultKind string `json:"result_kind"`
	apptypes.SearchProjectionGeneration
}

type projectionRebuildRunJSON struct {
	ResultKind string `json:"result_kind"`
	apptypes.SearchProjectionRunResult
}

func encodeProjectionRebuildGeneration(output io.Writer, got apptypes.SearchProjectionGeneration) error {
	return json.NewEncoder(output).Encode(projectionRebuildGenerationJSON{
		ResultKind:                 apptypes.SearchProjectionResultKindGeneration,
		SearchProjectionGeneration: got,
	})
}

func encodeProjectionRebuildRun(output io.Writer, got apptypes.SearchProjectionRunResult) error {
	return json.NewEncoder(output).Encode(projectionRebuildRunJSON{
		ResultKind:                apptypes.SearchProjectionResultKindRun,
		SearchProjectionRunResult: got,
	})
}

type storeProjectionBudgetInput struct {
	rows             int
	wall             time.Duration
	lock             time.Duration
	stored           int64
	decoded          int64
	written          int64
	recentAge        time.Duration
	indexFamilyBytes int64
}

func defaultStoreProjectionBudgetInput() storeProjectionBudgetInput {
	defaults := apptypes.DefaultSearchProjectionBudget()
	return storeProjectionBudgetInput{
		rows:             defaults.Rows,
		wall:             defaults.WallTime,
		lock:             defaults.LockTime,
		stored:           defaults.StoredBytes,
		decoded:          defaults.DecodedBytes,
		written:          defaults.WriteBytes,
		recentAge:        defaults.RecentAge,
		indexFamilyBytes: defaults.IndexFamilyBytes,
	}
}

func (in storeProjectionBudgetInput) budget() apptypes.SearchProjectionBudget {
	return apptypes.SearchProjectionBudget{
		Rows:             in.rows,
		WallTime:         in.wall,
		LockTime:         in.lock,
		StoredBytes:      in.stored,
		DecodedBytes:     in.decoded,
		WriteBytes:       in.written,
		RecentAge:        in.recentAge,
		IndexFamilyBytes: in.indexFamilyBytes,
	}
}
