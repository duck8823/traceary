package cli

import "time"

// event is the JSON shape of an event in CLI output.
type event struct {
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	Client     string `json:"client"`
	Agent      string `json:"agent"`
	SessionID  string `json:"session_id"`
	Workspace  string `json:"workspace"`
	Message    string `json:"message"`
	SourceHook string `json:"source_hook,omitempty"`
	CreatedAt  string `json:"created_at"`
}

// commandAudit is the JSON shape of a command audit in CLI output.
type commandAudit struct {
	Command         string `json:"command"`
	Input           string `json:"input"`
	Output          string `json:"output"`
	InputTruncated  bool   `json:"input_truncated"`
	OutputTruncated bool   `json:"output_truncated"`
	ExitCode        *int   `json:"exit_code,omitempty"`
}

// eventDetails is the JSON shape of an event-with-audit pair in CLI output.
type eventDetails struct {
	Event        event         `json:"event"`
	CommandAudit *commandAudit `json:"command_audit,omitempty"`
}

// contextOutput is the JSON shape produced by the `traceary context` command.
type contextOutput struct {
	ResolvedSessionID string  `json:"resolved_session_id,omitempty"`
	ResolvedWorkspace string  `json:"resolved_workspace,omitempty"`
	Events            []event `json:"events"`
}

// sessionTreeNode is the JSON shape of a single session in the tree output.
type sessionTreeNode struct {
	SessionID       string             `json:"session_id"`
	ParentSessionID string             `json:"parent_session_id,omitempty"`
	SpawnEventID    string             `json:"spawn_event_id,omitempty"`
	SubagentKind    string             `json:"subagent_kind,omitempty"`
	SpawnOrder      *int               `json:"spawn_order,omitempty"`
	Depth           int                `json:"depth"`
	Workspace       string             `json:"workspace,omitempty"`
	Label           string             `json:"label,omitempty"`
	Summary         string             `json:"summary,omitempty"`
	StartedAt       string             `json:"started_at"`
	EndedAt         *string            `json:"ended_at,omitempty"`
	Status          string             `json:"status"`
	DurationSec     *float64           `json:"duration_sec,omitempty"`
	TotalEvents     int                `json:"total_events"`
	CommandCount    int                `json:"command_count"`
	Agents          []string           `json:"agents"`
	SubagentType    string             `json:"subagent_type,omitempty"`
	Children        []*sessionTreeNode `json:"children"`
}

// memorySummaryOutput is the JSON shape of a durable memory summary in CLI output.
type memorySummaryOutput struct {
	MemoryID   string  `json:"memory_id"`
	Type       string  `json:"type"`
	ScopeKind  string  `json:"scope_kind"`
	ScopeValue string  `json:"scope_value"`
	Fact       string  `json:"fact"`
	Status     string  `json:"status"`
	Confidence string  `json:"confidence"`
	Source     string  `json:"source"`
	Supersedes *string `json:"supersedes,omitempty"`
	ExpiresAt  *string `json:"expires_at,omitempty"`
	ValidFrom  string  `json:"valid_from"`
	ValidTo    *string `json:"valid_to,omitempty"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
}

// memoryDetailsOutput is the JSON shape of a durable memory with refs.
type memoryDetailsOutput struct {
	Summary      memorySummaryOutput `json:"summary"`
	EvidenceRefs []string            `json:"evidence_refs"`
	ArtifactRefs []string            `json:"artifact_refs"`
}

// sessionSummaryOutput is the JSON shape of a session summary in list output.
type sessionSummaryOutput struct {
	SessionID       string   `json:"session_id"`
	Workspace       string   `json:"workspace,omitempty"`
	Label           string   `json:"label,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	ParentSessionID string   `json:"parent_session_id,omitempty"`
	SpawnEventID    string   `json:"spawn_event_id,omitempty"`
	SubagentKind    string   `json:"subagent_kind,omitempty"`
	SpawnOrder      *int     `json:"spawn_order,omitempty"`
	StartedAt       string   `json:"started_at"`
	EndedAt         *string  `json:"ended_at,omitempty"`
	Status          string   `json:"status"`
	DurationSec     *float64 `json:"duration_sec,omitempty"`
	TotalEvents     int      `json:"total_events"`
	CommandCount    int      `json:"command_count"`
	Agents          []string `json:"agents"`
}

// timelineWorkspaceBreakdownOutput is the JSON shape of a workspace within a timeline block.
type timelineWorkspaceBreakdownOutput struct {
	Workspace     string         `json:"workspace"`
	EventCount    int            `json:"event_count"`
	KindCounts    map[string]int `json:"kind_counts"`
	Agents        []string       `json:"agents"`
	Summary       string         `json:"summary"`
	SummarySource string         `json:"summary_source"`
}

// timelineBlockOutput is the JSON shape of a timeline block.
type timelineBlockOutput struct {
	Start              string                             `json:"start"`
	End                string                             `json:"end"`
	DurationSec        float64                            `json:"duration_sec"`
	EventCount         int                                `json:"event_count"`
	Workspaces         []string                           `json:"workspaces"`
	Agents             []string                           `json:"agents"`
	KindCounts         map[string]int                     `json:"kind_counts"`
	WorkspaceBreakdown []timelineWorkspaceBreakdownOutput `json:"workspace_breakdown"`
}

// bundleImportOutput is the JSON shape of a bundle import result.
type bundleImportOutput struct {
	SessionsImported      int `json:"sessions_imported"`
	SessionsSkipped       int `json:"sessions_skipped"`
	EventsImported        int `json:"events_imported"`
	EventsSkipped         int `json:"events_skipped"`
	CommandAuditsImported int `json:"command_audits_imported"`
	CommandAuditsSkipped  int `json:"command_audits_skipped"`
	MemoriesImported      int `json:"memories_imported"`
	MemoriesSkipped       int `json:"memories_skipped"`
	BundleSchemaVersion   int `json:"bundle_schema_version"`
}

// memoryHygieneApplyOutput is the JSON shape of a memory hygiene apply result.
type memoryHygieneApplyOutput struct {
	Applied  []memoryHygieneApplyAppliedOutput `json:"applied"`
	Failures []memoryHygieneApplyFailureOutput `json:"failures,omitempty"`
}

type memoryHygieneApplyAppliedOutput struct {
	MemoryID   string `json:"memory_id"`
	Kind       string `json:"kind"`
	Transition string `json:"transition"`
	Status     string `json:"status"`
}

type memoryHygieneApplyFailureOutput struct {
	MemoryID string `json:"memory_id"`
	Error    string `json:"error"`
}

// memoryHygieneScanOutput is the JSON shape of a memory hygiene scan result.
type memoryHygieneScanOutput struct {
	RedactionHitCount             int                        `json:"redaction_hit_count"`
	ExpiryCandidateCount          int                        `json:"expiry_candidate_count"`
	DuplicateCount                int                        `json:"duplicate_count"`
	SupersedeCandidateCount       int                        `json:"supersede_candidate_count"`
	ValidityOverlapSupersedeCount int                        `json:"validity_overlap_supersede_count"`
	Suggestions                   []memoryHygieneOutputEntry `json:"suggestions"`
}

// memoryInboxBatchOutput is the JSON shape of a memory inbox batch result.
type memoryInboxBatchOutput struct {
	Action    string                `json:"action"`
	Processed []memoryDetailsOutput `json:"processed"`
	Failures  []memoryInboxFailure  `json:"failures,omitempty"`
}

// memoryImportOutput is the JSON shape of a memory import result.
type memoryImportOutput struct {
	Imported              []memoryDetailsOutput `json:"imported"`
	SkippedDuplicateCount int                   `json:"skipped_duplicate_count"`
	SkippedRejectedCount  int                   `json:"skipped_rejected_count"`
	Warnings              []string              `json:"warnings,omitempty"`
}

// memoryExportOutput is the JSON shape of a memory export summary.
type memoryExportOutput struct {
	Target        string `json:"target"`
	ExportedCount int    `json:"exported_count"`
}

func formatJSONTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
