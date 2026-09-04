package cli

import "context"

const boundedDoctorDeferredSkipReasonEN = "skipped on stores at or above 2 GiB; default doctor stays inside the bounded-doctor contract (no event bodies, command payloads, identifier samples, or dbstat walk)"
const boundedDoctorDeferredSkipReasonJA = "2 GiB 以上の store では skip。default doctor は bounded-doctor 契約の範囲に留めます（event body、command payload、identifier sample、dbstat walk なし）"

// doctorInspectCallCheckNames maps inspect* calls appended on the default
// path after the large-store early return to the check names they emit.
// Bounded mode must include each name as a real status or an explicit skip.
var doctorInspectCallCheckNames = map[string][]string{
	"inspectPayloadCodec":                  {"payload-codec"},
	"inspectStoreGrowthBudget":             {"store-size"},
	"inspectTracearyOnPath":                {"path"},
	"inspectStaleTracearyProcesses":        {"stale-processes"},
	"inspectDoctorConfig":                  {"config"},
	"inspectSearchProjectionBudget":        {"search-projection-budget"},
	"inspectSearchProjectionParked":        {"search-projection-parked"},
	"inspectSearchProjectionTerminalRows":  {"search-projection-terminal-rows"},
	"inspectDedupeArchiveRuns":             {"dedupe-archive-runs"},
	"inspectStaleActiveSessions":           {"stale-active-sessions"},
	"inspectArchiveRetention":              {"archive-retention"},
	"inspectOfflineMigrations":             {"offline-migrations"},
	"inspectOneOffRepairs":                 {"one-off-repairs"},
	"inspectWorkspaceAliases":              {"workspace-aliases"},
	"inspectWorkspaceObservations":         {"workspace-observations"},
	"inspectCommandAuditReliability":       {"audit-reliability"},
	"inspectContentEventReliability":       {"content-event-reliability"},
	"inspectRetryLoops":                    {"retry-loops"},
	"inspectSensitiveAccessAuditCoverage":  {"sensitive-access-audit"},
	"inspectBodyCodec":                     {"body-codec"},
	"inspectAttestationAnchor":             {"attestation-anchor"},
	"inspectOperatorCost":                  {"store-operator-cost"},
	"inspectFileRetentionCapacity":         {"archive-capacity-retention", "backup-capacity-retention"},
	"inspectHookSpoolDiagnosticsFromScan":  {"hook-spool"},
	"inspectHookStateResidueMetadata":      {"hook-state-residue"},
	"inspectHookMemoryExtractDiagnostics":  {"hook-memory-extract"},
	"inspectMemoryInboxSaturation":         {"memory-inbox-saturation"},
	"inspectConsolidationConversion":       {"consolidation-conversion"},
	"inspectHookGrokTranscriptDiagnostics": {"hook-grok-transcript"},
}

func doctorChecksAppendedAfterLargeStoreReturn() []string {
	seen := map[string]struct{}{}
	names := make([]string, 0, len(doctorInspectCallCheckNames)+1)
	add := func(name string) {
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	add("db-write")
	for _, mapped := range doctorInspectCallCheckNames {
		for _, name := range mapped {
			add(name)
		}
	}
	return names
}

func skippedLargeStoreDoctorCheck(name string) doctorCheck {
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusSkip,
		Message: Localize(boundedDoctorDeferredSkipReasonEN, boundedDoctorDeferredSkipReasonJA),
	}
}

func (c *RootCLI) appendBoundedDoctorDeferredChecks(ctx context.Context, report *doctorReport) {
	if report == nil {
		return
	}
	inspectCtx, cancel := context.WithTimeout(ctx, largeStoreO1InspectTimeout)
	defer cancel()
	report.Checks = append(report.Checks, c.inspectConsolidationConversion(inspectCtx))
	present := map[string]struct{}{}
	for _, check := range report.Checks {
		present[check.Name] = struct{}{}
	}
	for _, name := range doctorChecksAppendedAfterLargeStoreReturn() {
		if _, ok := present[name]; ok {
			continue
		}
		report.Checks = append(report.Checks, skippedLargeStoreDoctorCheck(name))
	}
}
