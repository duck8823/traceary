package application

import (
	"time"

	"github.com/duck8823/traceary/application/types"
)

// EvaluatePayloadActivationReadiness applies the activation policy to explicit
// evidence supplied by adapters and operators. Missing evidence remains
// unknown; compatibility is never inferred merely because an inspection ran.
func EvaluatePayloadActivationReadiness(m types.PayloadRehearsalMetrics) types.PayloadActivationReadiness {
	e := m.ActivationReadiness
	normalize := func(s types.ReadinessGateStatus) types.ReadinessGateStatus {
		if s == "" {
			return types.ReadinessUnknown
		}
		return s
	}
	e.MinimumReaderStatus = normalize(e.MinimumReaderStatus)
	e.OldProcessesStoppedStatus = normalize(e.OldProcessesStoppedStatus)
	e.BackupStatus = normalize(e.BackupStatus)
	e.HeadroomStatus = normalize(e.HeadroomStatus)
	e.ScrubStatus = normalize(e.ScrubStatus)
	e.RollbackStatus = normalize(e.RollbackStatus)
	status := func(proved bool, supplied types.ReadinessGateStatus) types.ReadinessGateStatus {
		if supplied != types.ReadinessUnknown {
			return supplied
		}
		if proved {
			return types.ReadinessPassed
		}
		return types.ReadinessUnknown
	}
	e.LiveIdentityOnly = m.LiveIdentityOnly
	e.BackupVerified = m.RollbackVerified
	e.HeadroomSufficient = m.EstimatedHeadroom > 0 && m.FreeBytes >= m.EstimatedHeadroom
	e.RehearsalComplete = m.State == string(types.PayloadRehearsalCompleted) || m.State == string(types.PayloadRehearsalScrubbed)
	e.ScrubPassed = m.State == string(types.PayloadRehearsalScrubbed)
	e.RollbackVerified = m.State == string(types.PayloadRehearsalRolledBack) && m.RollbackVerified
	e.BackupStatus = status(e.BackupVerified, e.BackupStatus)
	e.HeadroomStatus = status(e.HeadroomSufficient, e.HeadroomStatus)
	e.ScrubStatus = status(e.ScrubPassed, e.ScrubStatus)
	e.RollbackStatus = status(e.RollbackVerified, e.RollbackStatus)
	// MinimumReaderStatus and OldProcessesStoppedStatus require evidence from
	// outside SQLite. Do not manufacture it from store compatibility facts.
	e.CompatibleReader = e.MinimumReaderStatus == types.ReadinessPassed
	e.ActivationAllowed = e.CompatibleReader && e.LiveIdentityOnly && e.BackupVerified && e.HeadroomSufficient && e.RehearsalComplete && e.ScrubPassed && e.RollbackVerified && e.OldProcessesStoppedStatus == types.ReadinessPassed
	if e.EvidenceAt.IsZero() && (e.BackupStatus != types.ReadinessUnknown || e.HeadroomStatus != types.ReadinessUnknown || e.ScrubStatus != types.ReadinessUnknown || e.RollbackStatus != types.ReadinessUnknown || e.MinimumReaderStatus != types.ReadinessUnknown || e.OldProcessesStoppedStatus != types.ReadinessUnknown) {
		e.EvidenceAt = time.Now().UTC()
	}
	return e
}
