package types

import (
	"fmt"
	"strconv"
	"strings"
)

// OfflineMigrationsRequiredError is returned when implicit store open would
// apply a data_dependent_offline migration against a store that already has
// source events. The store is not modified. Operators apply those migrations
// with `traceary doctor --fix`.
type OfflineMigrationsRequiredError struct {
	Versions []int64
}

func (e *OfflineMigrationsRequiredError) Error() string {
	if e == nil || len(e.Versions) == 0 {
		return "store has unapplied data-dependent migrations; run `traceary doctor --fix`"
	}
	parts := make([]string, 0, len(e.Versions))
	for _, version := range e.Versions {
		parts = append(parts, strconv.FormatInt(version, 10))
	}
	return fmt.Sprintf("store has unapplied data-dependent migrations (%s); run `traceary doctor --fix`", strings.Join(parts, ", "))
}

const (
	// OneOffRepairOutstanding means remaining work exists and the completion
	// record is not in the ledger.
	OneOffRepairOutstanding = "outstanding"
	// OneOffRepairNeverRan means the offline version is pending and no
	// remaining work exists.
	OneOffRepairNeverRan = "never-ran"
	// OneOffRepairRetired means the completion record is present.
	OneOffRepairRetired = "retired"
)

// OneOffRepairRetirement is the per-repair outstanding/retired view doctor
// prints so "it never ran" and "it finished" are distinguishable.
type OneOffRepairRetirement struct {
	Epoch     string
	Workspace string
}
