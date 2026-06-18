package usecase

import (
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// SummarizeCandidateHygiene classifies the candidate memories in the supplied
// summaries into the hygiene dimensions surfaced by the snapshot (#1169). Only
// rows with status candidate are considered; accepted/other rows are ignored so
// callers can pass a mixed slice (e.g. the reliability scan loads accepted +
// candidate together).
//
// The four flag dimensions are independent and may overlap; LikelyActionable is
// the complement (flagged by none). Duplicate detection is exact only, keyed by
// the same scope + memory type + fact identity the extraction dedupe uses
// (memoryCandidateKey), so identical text in different workspaces or memory
// types is not falsely merged — the reliability scan is global by default.
// Similarity duplicates stay in the hygiene scan. The classification reuses
// classifyExtractionNoise so it stays consistent with the extraction-time gate
// and the cleanup classifier.
func SummarizeCandidateHygiene(candidates []apptypes.MemorySummary, now time.Time, staleThreshold time.Duration) apptypes.CandidateHygieneCounts {
	identityCounts := make(map[string]int, len(candidates))
	for _, summary := range candidates {
		if summary.Status() != domtypes.MemoryStatusCandidate {
			continue
		}
		identityCounts[memoryCandidateKey(summary.Scope(), summary.MemoryType(), summary.Fact())]++
	}

	var counts apptypes.CandidateHygieneCounts
	for _, summary := range candidates {
		if summary.Status() != domtypes.MemoryStatusCandidate {
			continue
		}
		flagged := false
		if staleThreshold > 0 && now.Sub(summary.UpdatedAt()) > staleThreshold {
			counts.Stale++
			flagged = true
		}
		if identityCounts[memoryCandidateKey(summary.Scope(), summary.MemoryType(), summary.Fact())] > 1 {
			counts.Duplicate++
			flagged = true
		}
		if isFragmentLikeNoise(classifyExtractionNoise(summary.Fact())) {
			counts.FragmentLike++
			flagged = true
		}
		if summary.Source() == domtypes.MemorySourceExtractedHidden {
			counts.ExtractedHidden++
			flagged = true
		}
		if !flagged {
			counts.LikelyActionable++
		}
	}
	return counts
}
