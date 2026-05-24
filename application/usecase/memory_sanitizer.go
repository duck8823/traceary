package usecase

import (
	"strings"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application/redaction"
	domtypes "github.com/duck8823/traceary/domain/types"
)

// sanitizeMemoryPayload trims the fact, masks secrets via the built-in audit
// redaction rules, and applies any caller-supplied extra patterns. It is
// shared between the durable-memory lifecycle usecase and the import usecase
// so host-local content (for example, ~/.codex/memories files) goes through
// the same redaction pass as any other memory write before it is persisted.
func sanitizeMemoryPayload(
	fact string,
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
	extraRedactPatterns []string,
) (string, []domtypes.EvidenceRef, []domtypes.ArtifactRef, error) {
	sanitizedEvidenceRefs, sanitizedArtifactRefs, extraRedactors, err := sanitizeMemoryRefsWithRedactors(evidenceRefs, artifactRefs, extraRedactPatterns)
	if err != nil {
		return "", nil, nil, err
	}

	sanitizedFact, _ := redaction.Apply(strings.TrimSpace(fact), extraRedactors)
	return sanitizedFact, sanitizedEvidenceRefs, sanitizedArtifactRefs, nil
}

func sanitizeMemoryRefs(
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
	extraRedactPatterns []string,
) ([]domtypes.EvidenceRef, []domtypes.ArtifactRef, error) {
	sanitizedEvidenceRefs, sanitizedArtifactRefs, _, err := sanitizeMemoryRefsWithRedactors(evidenceRefs, artifactRefs, extraRedactPatterns)
	return sanitizedEvidenceRefs, sanitizedArtifactRefs, err
}

func sanitizeMemoryRefsWithRedactors(
	evidenceRefs []domtypes.EvidenceRef,
	artifactRefs []domtypes.ArtifactRef,
	extraRedactPatterns []string,
) ([]domtypes.EvidenceRef, []domtypes.ArtifactRef, []redaction.Redactor, error) {
	extraRedactors, err := redaction.CompileExtraPatterns(extraRedactPatterns)
	if err != nil {
		return nil, nil, nil, xerrors.Errorf("failed to compile extra redaction patterns: %w", err)
	}

	sanitizedEvidenceRefs, err := sanitizeEvidenceRefs(evidenceRefs, extraRedactors)
	if err != nil {
		return nil, nil, nil, err
	}
	sanitizedArtifactRefs, err := sanitizeArtifactRefs(artifactRefs, extraRedactors)
	if err != nil {
		return nil, nil, nil, err
	}

	return sanitizedEvidenceRefs, sanitizedArtifactRefs, extraRedactors, nil
}
