package cli

import apptypes "github.com/duck8823/traceary/application/types"

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
