package types

import (
	"fmt"
	"strconv"
	"strings"
)

// BoundDropInspection is the preflight count and identity digest of rows
// that a pending retired-table drop would remove.
type BoundDropInspection struct {
	RowCount int64
	Digest   string
}

// BoundDropApprovalRequiredError stops an upgrade that would drop rows
// without a matching bound approval. The live store is left untouched.
type BoundDropApprovalRequiredError struct {
	RowCount int64
	Digest   string
}

func (e *BoundDropApprovalRequiredError) Error() string {
	if e == nil {
		return FormatBoundDropPreflight(0, "")
	}
	return FormatBoundDropPreflight(e.RowCount, e.Digest)
}

// FormatBoundDropPreflight is the operator-facing stop text for a pending
// drop of the retired run_lineages table.
func FormatBoundDropPreflight(rowCount int64, digest string) string {
	return fmt.Sprintf(
		"run_lineages: %d rows would be dropped (identities digest sha256:%s); retrieve lineage facts first with the 0.48.2 binary (bundle export includes run_lineages.ndjson); re-run this command with explicit approval of %d:%s to proceed",
		rowCount, digest, rowCount, digest,
	)
}

// ParseBoundDropApprovalToken parses the N:hex token printed by preflight.
func ParseBoundDropApprovalToken(value string) (int64, string, error) {
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
