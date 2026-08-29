package cli

import (
	"context"

	apptypes "github.com/duck8823/traceary/application/types"
)

const exactRedeliveryRateTarget = 0.01

// doctorWorkspaceIdentity is the additive doctor --json block that replaces
// the retired `report workspace-identity` leaf. Fields match the former
// workspace_identity object plus derived exact_delivery. Heuristic body
// scans are not part of this absorb.
type doctorWorkspaceIdentity struct {
	Coverage          apptypes.WorkspaceIdentityCoverage       `json:"coverage"`
	ConflictPairCount int                                      `json:"conflict_pair_count"`
	Sources           []apptypes.WorkspaceIdentitySourceReport `json:"sources"`
	ConflictSamples   []apptypes.WorkspaceConflictSample       `json:"conflict_samples"`
	Aliases           []apptypes.WorkspaceAliasSummary         `json:"aliases"`
	ExactDelivery     workspaceExactDeliverySummary            `json:"exact_delivery"`
	ObservationRows   int                                      `json:"observation_rows"`
	ObservationKeys   int                                      `json:"observation_keys"`
	OrphanRows        int                                      `json:"orphan_rows"`
}

type workspaceExactDeliverySummary struct {
	AttemptCount         int     `json:"attempt_count"`
	ExactRedeliveryCount int     `json:"exact_redelivery_count"`
	ExactRedeliveryRate  float64 `json:"exact_redelivery_rate"`
	TargetRate           float64 `json:"target_rate"`
	SampleAvailable      bool    `json:"sample_available"`
	TargetMet            bool    `json:"target_met"`
}

func newDoctorWorkspaceIdentity(identity apptypes.WorkspaceIdentityReport) *doctorWorkspaceIdentity {
	sources := identity.Sources
	if sources == nil {
		sources = []apptypes.WorkspaceIdentitySourceReport{}
	}
	samples := identity.ConflictSamples
	if samples == nil {
		samples = []apptypes.WorkspaceConflictSample{}
	}
	aliases := identity.Aliases
	if aliases == nil {
		aliases = []apptypes.WorkspaceAliasSummary{}
	}
	return &doctorWorkspaceIdentity{
		Coverage:          identity.Coverage,
		ConflictPairCount: identity.ConflictPairCount,
		Sources:           sources,
		ConflictSamples:   samples,
		Aliases:           aliases,
		ExactDelivery:     buildWorkspaceExactDeliverySummary(identity),
		ObservationRows:   identity.Coverage.ObservationRows,
		ObservationKeys:   identity.Coverage.ObservationKeys,
		OrphanRows:        identity.Coverage.OrphanObservationRows,
	}
}

func skippedWorkspaceObservationsCheck() doctorCheck {
	return doctorCheck{
		Name:   "workspace-observations",
		Status: doctorStatusSkip,
		Message: Localize(
			"default doctor is filesystem-metadata-only for stores at or above 2 GiB; use doctor --json on a reviewed copy",
			"2 GiB 以上の store では default doctor は filesystem metadata のみです。review 済み copy で doctor --json を使ってください",
		),
	}
}

func (c *RootCLI) inspectWorkspaceObservations(ctx context.Context, report *doctorReport) doctorCheck {
	const name = "workspace-observations"
	if c.workspaceIdentity == nil {
		return skippedWorkspaceObservationsCheck()
	}
	identity, err := c.workspaceIdentity.Report(ctx, doctorWorkspaceAliasConflictSampleLimit)
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("workspace observation footprint failed: %v", "workspace observation の確認に失敗しました: %v", err),
		}
	}
	if report != nil {
		report.WorkspaceIdentity = newDoctorWorkspaceIdentity(identity)
	}
	dbPath := ""
	if report != nil {
		dbPath = report.hintDBPath
	}
	return workspaceObservationsCheck(name, dbPath, identity.Coverage)
}

func workspaceObservationsCheck(name, dbPath string, coverage apptypes.WorkspaceIdentityCoverage) doctorCheck {
	if coverage.PreCollapse {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("pre-collapse shape: rows=%d keys=%d volume=%d", "collapse 前の形です: rows=%d keys=%d volume=%d", coverage.ObservationRows, coverage.ObservationKeys, coverage.ObservationCount),
			Hint: Localize(
				"session_workspace_observations still grows one row per event; apply the offline collapse",
				"session_workspace_observations はまだ event ごとに 1 行増えます。offline collapse を適用してください",
			),
			FixCommand: "traceary doctor --fix",
		}
	}
	if coverage.ObservationRows != coverage.ObservationKeys {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("rows=%d keys=%d volume=%d (rows should equal keys after collapse)", "rows=%d keys=%d volume=%d（collapse 後は rows と keys が一致する必要があります）", coverage.ObservationRows, coverage.ObservationKeys, coverage.ObservationCount),
			Hint: Localize(
				"apply traceary doctor --fix if the store is still on the pre-collapse shape",
				"まだ collapse 前なら traceary doctor --fix を適用してください",
			),
			FixCommand: "traceary doctor --fix",
		}
	}
	if coverage.OrphanObservationRows > 0 {
		fix := "traceary store compact"
		if dbPath != "" {
			fix = "traceary store compact --db-path " + shellQuote(dbPath)
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("orphan observations: rows=%d keys=%d volume=%d orphans=%d", "到達不能な observation があります: rows=%d keys=%d volume=%d orphans=%d", coverage.ObservationRows, coverage.ObservationKeys, coverage.ObservationCount, coverage.OrphanObservationRows),
			Hint: Localize(
				"observations whose session and events are gone still occupy rows; compact deletes them",
				"session も event も無い observation が残っています。compact が削除します",
			),
			FixCommand: fix,
		}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusPass,
		Message: localizef("rows=%d keys=%d volume=%d orphans=0", "rows=%d keys=%d volume=%d orphans=0", coverage.ObservationRows, coverage.ObservationKeys, coverage.ObservationCount),
	}
}

func buildWorkspaceExactDeliverySummary(identity apptypes.WorkspaceIdentityReport) workspaceExactDeliverySummary {
	attempts, exact := 0, 0
	for _, source := range identity.Sources {
		attempts += source.RuntimeAttemptCount
		exact += source.ExactRedeliveryCount
	}
	rate := reportRatio(exact, attempts)
	return workspaceExactDeliverySummary{
		AttemptCount:         attempts,
		ExactRedeliveryCount: exact,
		ExactRedeliveryRate:  rate,
		TargetRate:           exactRedeliveryRateTarget,
		SampleAvailable:      attempts > 0,
		TargetMet:            attempts > 0 && rate < exactRedeliveryRateTarget,
	}
}

func reportRatio(numerator, denominator int) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}
