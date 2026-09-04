package domain

import (
	"fmt"
	"strings"
)

// BoundDropApproval is the immutable evidence that an operator approved
// dropping a specific identity set from a pending offline table drop.
// Absence of this value on PreparedStoreUpgradeRun is the unapproved state.
type BoundDropApproval struct {
	RowCount       int64  `json:"row_count"`
	IdentityDigest string `json:"identity_digest"`
}

// NewBoundDropApproval validates a bound drop approval.
func NewBoundDropApproval(rowCount int64, identityDigest string) (BoundDropApproval, error) {
	digest := strings.TrimSpace(identityDigest)
	if rowCount < 0 {
		return BoundDropApproval{}, fmt.Errorf("bound drop approval row count must not be negative")
	}
	if digest == "" {
		return BoundDropApproval{}, fmt.Errorf("bound drop approval identity digest must not be empty")
	}
	return BoundDropApproval{RowCount: rowCount, IdentityDigest: digest}, nil
}

// Matches reports whether the approval still describes the inspected set.
func (a BoundDropApproval) Matches(rowCount int64, identityDigest string) bool {
	return a.RowCount == rowCount && a.IdentityDigest == identityDigest
}
