package types

import (
	"time"

	domtypes "github.com/duck8823/traceary/domain/types"
)

// OneShotRepairEvidenceSource identifies the authoritative process-exit record
// supplied by the operator. These values never include transcript inference.
type OneShotRepairEvidenceSource string

const (
	// OneShotRepairEvidenceSupervisedProcess is emitted by Traceary's process supervisor.
	OneShotRepairEvidenceSupervisedProcess OneShotRepairEvidenceSource = "supervised_process_exit"
	// OneShotRepairEvidenceCodexExec identifies an observed Codex exec process exit.
	OneShotRepairEvidenceCodexExec OneShotRepairEvidenceSource = "codex_exec_exit"
	// OneShotRepairEvidenceBatchRunner identifies a batch runner's per-child exit record.
	OneShotRepairEvidenceBatchRunner OneShotRepairEvidenceSource = "batch_runner_exit"
	// OneShotRepairEvidenceOperatorAttested identifies an explicit operator attestation.
	OneShotRepairEvidenceOperatorAttested OneShotRepairEvidenceSource = "operator_attested_process_exit"
)

// OneShotRepairEvidenceEntry is one operator-supplied terminal attestation.
type OneShotRepairEvidenceEntry struct {
	SessionID      domtypes.SessionID
	RuntimeMode    domtypes.RuntimeMode
	TerminalReason domtypes.TerminalReason
	CompletedAt    time.Time
	EvidenceSource OneShotRepairEvidenceSource
	EvidenceRef    string
}

// OneShotRepairParams defines one evidence-backed preview or apply operation.
type OneShotRepairParams struct {
	Entries      []OneShotRepairEvidenceEntry
	EvidenceHash string
	StaleAfter   time.Duration
	Now          time.Time
}

// OneShotRepairApplyParams requires a rollback snapshot for a write operation.
type OneShotRepairApplyParams struct {
	Repair     OneShotRepairParams
	BackupPath string
}

// OneShotRepairStats reports lifecycle counts across the store snapshot.
type OneShotRepairStats struct {
	ActiveCount    int `json:"active_count"`
	StaleCount     int `json:"stale_count"`
	CompletedCount int `json:"completed_count"`
	FailedCount    int `json:"failed_count"`
}

// OneShotRepairCandidate explains the decision for one evidence entry.
type OneShotRepairCandidate struct {
	SessionID         domtypes.SessionID          `json:"session_id"`
	StoredRuntimeMode domtypes.RuntimeMode        `json:"stored_runtime_mode,omitempty"`
	ProposedReason    domtypes.TerminalReason     `json:"proposed_terminal_reason"`
	CompletedAt       time.Time                   `json:"completed_at"`
	LatestActivityAt  time.Time                   `json:"latest_activity_at,omitempty"`
	EvidenceSource    OneShotRepairEvidenceSource `json:"evidence_source"`
	EvidenceRef       string                      `json:"evidence_ref"`
	Eligible          bool                        `json:"eligible"`
	Applied           bool                        `json:"applied"`
	Decision          string                      `json:"decision"`
}

// OneShotRepairResult is the complete explainable outcome of one run.
type OneShotRepairResult struct {
	EvidenceHash string                   `json:"evidence_hash"`
	ApplyMode    bool                     `json:"apply_mode"`
	Before       OneShotRepairStats       `json:"before"`
	After        OneShotRepairStats       `json:"after"`
	Candidates   []OneShotRepairCandidate `json:"candidates"`
}

// AppliedCount returns the number of terminal transitions committed by this run.
func (r OneShotRepairResult) AppliedCount() int {
	count := 0
	for _, candidate := range r.Candidates {
		if candidate.Applied {
			count++
		}
	}
	return count
}
