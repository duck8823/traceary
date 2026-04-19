package usecase

import (
	"strings"

	"golang.org/x/xerrors"

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
	extraRedactors, err := compileExtraRedactPatterns(extraRedactPatterns)
	if err != nil {
		return "", nil, nil, xerrors.Errorf("failed to compile extra redaction patterns: %w", err)
	}

	sanitizedFact, _ := redactAuditPayload(strings.TrimSpace(fact), extraRedactors)
	sanitizedEvidenceRefs, err := sanitizeEvidenceRefs(evidenceRefs, extraRedactors)
	if err != nil {
		return "", nil, nil, err
	}
	sanitizedArtifactRefs, err := sanitizeArtifactRefs(artifactRefs, extraRedactors)
	if err != nil {
		return "", nil, nil, err
	}

	return sanitizedFact, sanitizedEvidenceRefs, sanitizedArtifactRefs, nil
}
