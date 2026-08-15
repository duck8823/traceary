package application

import (
	"os"
	"path/filepath"

	"golang.org/x/xerrors"
)

// Release-gate rows that can still trip a release (#1873 / #1620).
const (
	ReleaseGateEmissionAmplificationMax    = 2.0
	ReleaseGateWholeStoreAmplificationMax  = 3.0
	ReleaseGateRecentIndexAmplificationMax = 4.0
	ReleaseGateBodyDuplicateShareMax       = 0.05
	ReleaseGateSchemaVersion               = "traceary.release_gate/v1"
	ReleaseGateMeasurementCorpus           = "maintainer store 2026-08-11 uncompressed #1620"

	ReleaseGateStatusPass = "pass"
	ReleaseGateStatusMiss = "miss"
	ReleaseGateStatusSkip = "skip"

	ReleaseGateKindRatio    = "ratio"
	ReleaseGateKindCoverage = "coverage"
	ReleaseGateKindBoolean  = "boolean"
)

// DefaultLiveStorePath is the operator's default file. Evaluators refuse it.
func DefaultLiveStorePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("failed to resolve home directory: %w", err)
	}
	path, err := filepath.Abs(filepath.Join(home, ".config", "traceary", "traceary.db"))
	if err != nil {
		return "", xerrors.Errorf("failed to resolve live store path: %w", err)
	}
	return path, nil
}

// PathsReferToSameStore reports whether candidate is the default live file.
func PathsReferToSameStore(candidate, live string) (bool, error) {
	if filepath.Clean(candidate) == filepath.Clean(live) {
		return true, nil
	}
	evalCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		evalCandidate = candidate
	}
	evalLive, err := filepath.EvalSymlinks(live)
	if err != nil {
		evalLive = live
	}
	if filepath.Clean(evalCandidate) == filepath.Clean(evalLive) {
		return true, nil
	}
	candidateInfo, candidateErr := os.Stat(candidate)
	liveInfo, liveErr := os.Stat(live)
	if candidateErr != nil || liveErr != nil {
		return false, nil
	}
	return os.SameFile(candidateInfo, liveInfo), nil
}

// RefuseLiveStore returns an error when path is the default live store.
func RefuseLiveStore(path string) error {
	resolved, err := filepath.Abs(path)
	if err != nil {
		return xerrors.Errorf("failed to resolve store path: %w", err)
	}
	live, err := DefaultLiveStorePath()
	if err != nil {
		return err
	}
	same, err := PathsReferToSameStore(resolved, live)
	if err != nil {
		return err
	}
	if same {
		return xerrors.Errorf("refusing the default live store %s; pass a fixture or operator copy", live)
	}
	return nil
}

// ClassifyUpperBound is pass when observed <= max, miss otherwise.
// Unmeasured observations skip instead of failing.
func ClassifyUpperBound(measured bool, observed, limit float64) string {
	if !measured {
		return ReleaseGateStatusSkip
	}
	if observed <= limit {
		return ReleaseGateStatusPass
	}
	return ReleaseGateStatusMiss
}

// ClassifyStrictUpperBound is pass when observed < limit, miss otherwise.
func ClassifyStrictUpperBound(measured bool, observed, limit float64) string {
	if !measured {
		return ReleaseGateStatusSkip
	}
	if observed < limit {
		return ReleaseGateStatusPass
	}
	return ReleaseGateStatusMiss
}

// ClassifyLowerBound is pass when observed >= limit, miss otherwise.
func ClassifyLowerBound(measured bool, observed, limit float64) string {
	if !measured {
		return ReleaseGateStatusSkip
	}
	if observed+1e-12 >= limit {
		return ReleaseGateStatusPass
	}
	return ReleaseGateStatusMiss
}
