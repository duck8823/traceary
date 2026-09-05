package types

import (
	"fmt"
	"strconv"
	"strings"
)

// UnavailableRetentionInlineIDBound is the maximum number of affected event
// ids printed inline by preflight and doctor. Above this bound only the count
// and the sorted-id-set digest are printed.
const UnavailableRetentionInlineIDBound = 16

// UnavailableRetentionInspection is the preflight count, bounded sample and
// sorted-id-set digest of rows still marked unavailable_retention.
type UnavailableRetentionInspection struct {
	RowCount      int64
	Digest        string
	Sample        []string
	SchemaVersion int64
}

// UnavailableRetentionApprovalRequiredError stops an upgrade that would drop
// unavailable_retention marker rows without a matching bound approval. The
// live store is left untouched. Those bodies cannot be recovered by any
// binary, including 0.48.2.
type UnavailableRetentionApprovalRequiredError struct {
	RowCount      int64
	Digest        string
	Sample        []string
	SchemaVersion int64
}

func (e *UnavailableRetentionApprovalRequiredError) Error() string {
	if e == nil {
		return FormatUnavailableRetentionPreflight(UnavailableRetentionInspection{})
	}
	return FormatUnavailableRetentionPreflight(UnavailableRetentionInspection{
		RowCount:      e.RowCount,
		Digest:        e.Digest,
		Sample:        e.Sample,
		SchemaVersion: e.SchemaVersion,
	})
}

// FormatUnavailableRetentionPreflight is the operator-facing stop text for a
// pending drop of unavailable_retention marker rows.
func FormatUnavailableRetentionPreflight(inspection UnavailableRetentionInspection) string {
	ids := formatUnavailableRetentionSample(inspection.Sample, inspection.RowCount)
	return fmt.Sprintf(
		"unavailable_retention: %d event bodies were discarded and cannot be recovered by any binary, including 0.48.2 (sorted-id-set digest sha256:%s, schema version %d)%s; re-run this command with explicit approval of %d:%s to drop the markers and the column",
		inspection.RowCount, inspection.Digest, inspection.SchemaVersion, ids, inspection.RowCount, inspection.Digest,
	)
}

func formatUnavailableRetentionSample(sample []string, rowCount int64) string {
	if rowCount <= 0 || len(sample) == 0 {
		return ""
	}
	if rowCount > UnavailableRetentionInlineIDBound {
		return ""
	}
	return "; ids: " + strings.Join(sample, ",")
}

// ParseUnavailableRetentionApprovalToken parses the N:hex token printed by preflight.
func ParseUnavailableRetentionApprovalToken(value string) (int64, string, error) {
	value = strings.TrimSpace(value)
	countText, digest, ok := strings.Cut(value, ":")
	if !ok || countText == "" || digest == "" {
		return 0, "", fmt.Errorf("approval must be N:<hex digest>")
	}
	count, err := strconv.ParseInt(countText, 10, 64)
	if err != nil || count < 0 {
		return 0, "", fmt.Errorf("approval must be N:<hex digest>")
	}
	return count, digest, nil
}
