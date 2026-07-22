package cli

import "time"

// event is the JSON shape of an event in CLI output.
//
// Truncated / MessageLength / MessageBytes are additive metadata
// introduced for the shared recent-command truncation policy. They are
// populated only when the snapshot renderer cut the message, so legacy
// consumers that destructure `message` continue to work and explicit
// detail lookups (`traceary show`) that bypass truncation never emit
// these keys.
type event struct {
	EventID               string `json:"event_id"`
	Kind                  string `json:"kind"`
	Client                string `json:"client"`
	Agent                 string `json:"agent"`
	SessionID             string `json:"session_id"`
	Workspace             string `json:"workspace"`
	Message               string `json:"message"`
	BodyUnavailableReason string `json:"body_unavailable_reason,omitempty"`
	SourceHook            string `json:"source_hook,omitempty"`
	CreatedAt             string `json:"created_at"`
	Truncated             bool   `json:"truncated,omitempty"`
	MessageLength         int    `json:"message_length,omitempty"`
	MessageBytes          int    `json:"message_bytes,omitempty"`
}

// commandAudit is the JSON shape of a command audit in CLI output.
type commandAudit struct {
	Command             string                   `json:"command"`
	Wrapper             string                   `json:"wrapper,omitempty"`
	CommandName         string                   `json:"command_name"`
	Input               string                   `json:"input"`
	Output              string                   `json:"output"`
	InputTruncated      bool                     `json:"input_truncated"`
	OutputTruncated     bool                     `json:"output_truncated"`
	InputOriginalBytes  int                      `json:"input_original_bytes,omitempty"`
	OutputOriginalBytes int                      `json:"output_original_bytes,omitempty"`
	ExitCode            *int                     `json:"exit_code,omitempty"`
	Failed              bool                     `json:"failed,omitempty"`
	FailureReason       string                   `json:"failure_reason"`
	Sensitive           *sensitiveClassification `json:"sensitive,omitempty"`
}

// sensitiveClassification is the separable sensitive-path claim (not redaction).
type sensitiveClassification struct {
	Matched     bool   `json:"matched"`
	Class       string `json:"class,omitempty"`
	Operation   string `json:"operation,omitempty"`
	Evidence    string `json:"evidence,omitempty"`
	Coverage    string `json:"coverage,omitempty"`
	Redaction   string `json:"redaction,omitempty"`
	MatchedPath string `json:"matched_path,omitempty"`
	IntentOnly  bool   `json:"intent_only"`
	Summary     string `json:"summary,omitempty"`
	CoverageGap string `json:"coverage_gap,omitempty"`
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

// topSnapshotPayload is the top-level JSON shape of
// `traceary sessions --snapshot --json` / `traceary top --snapshot --json`. The envelope was introduced in
// v0.14.0 alongside the multi-pane redesign so the snapshot can carry
// the dashboard's secondary surfaces (recent failures, recent
// commands, memory review queue candidates) next to the active
// session tree. Earlier releases emitted a bare top-level array of
// session nodes; the inner session shape is unchanged so consumers
// that already destructure `sessions[*]` keep working — only the
// outer wrapping is new.
type topSnapshotPayload struct {
	// Profile names the JSON projection. Omitted for the default operator
	// envelope so existing consumers keep an unchanged shape; set to "ai"
	// for the bounded agent-resume projection.
	Profile        string                 `json:"profile,omitempty"`
	Sessions       []*topSnapshotNode     `json:"sessions"`
	Failures       []event                `json:"failures"`
	RecentCommands []event                `json:"recent_commands"`
	Candidates     topSnapshotCandidates  `json:"candidates"`
	StaleMemories  topSnapshotStale       `json:"stale_memories"`
	Reliability    topSnapshotReliability `json:"reliability"`
}

// topSnapshotCandidates wraps the durable-memory inbox slice with an
// explicit count so JSON consumers do not have to read len(items) to
// learn how many candidates the snapshot returned. The number reflects
// the rows included in the snapshot (capped by the per-pane limit), so
// it is the same value the dashboard renders in the candidates pane.
type topSnapshotCandidates struct {
	Count               int                   `json:"count"`
	RememberIntentCount int                   `json:"remember_intent_count"`
	Items               []memorySummaryOutput `json:"items"`
}

// topSnapshotStale wraps stale durable-memory rows with the total stale count
// before the per-pane item cap is applied.
type topSnapshotStale struct {
	Count int                 `json:"count"`
	Items []staleMemoryOutput `json:"items"`
}

// topSnapshotReliability groups additive dogfood reliability signals for
// operator cockpit and script consumers. Counts are derived from the same
// filtered snapshot window as the dashboard so existing consumers can ignore
// the key without changing their session / event parsing.
type topSnapshotReliability struct {
	StaleActiveSessionCount int                          `json:"stale_active_session_count"`
	Memory                  topSnapshotReliabilityMemory `json:"memory"`
	CandidateAge            topSnapshotCandidateAge      `json:"candidate_age"`
	LargePayloads           topSnapshotLargePayloads     `json:"large_payloads"`
}

type topSnapshotReliabilityMemory struct {
	AcceptedCount    int                         `json:"accepted_count"`
	CandidateCount   int                         `json:"candidate_count"`
	AcceptedRatio    *float64                    `json:"accepted_ratio,omitempty"`
	ScanLimit        int                         `json:"scan_limit"`
	ScanLimitReached bool                        `json:"scan_limit_reached"`
	CandidateHygiene topSnapshotCandidateHygiene `json:"candidate_hygiene"`
}

// topSnapshotCandidateHygiene reports the hygiene composition of the scanned
// candidate window (#1169). The four flag counts are independent diagnostic
// dimensions and may overlap; likely_actionable_count is the complement
// (candidates flagged by none). Counts are subject to scan_limit_reached.
type topSnapshotCandidateHygiene struct {
	StaleCount            int `json:"stale_count"`
	DuplicateCount        int `json:"duplicate_count"`
	FragmentLikeCount     int `json:"fragment_like_count"`
	ExtractedHiddenCount  int `json:"extracted_hidden_count"`
	LikelyActionableCount int `json:"likely_actionable_count"`
}

type topSnapshotCandidateAge struct {
	Count             int     `json:"count"`
	OldestUpdatedAt   *string `json:"oldest_updated_at,omitempty"`
	NewestUpdatedAt   *string `json:"newest_updated_at,omitempty"`
	OldestAgeSeconds  *int64  `json:"oldest_age_seconds,omitempty"`
	AverageAgeSeconds *int64  `json:"average_age_seconds,omitempty"`
}

type topSnapshotLargePayloads struct {
	Count              int                             `json:"count"`
	RecentCommandCount int                             `json:"recent_command_count"`
	RecentFailureCount int                             `json:"recent_failure_count"`
	SampledEventCount  int                             `json:"sampled_event_count"`
	BodyLimitRunes     int                             `json:"body_limit_runes"`
	Samples            []topSnapshotLargePayloadSample `json:"samples,omitempty"`
}

// topSnapshotLargePayloadSample carries body-safe metadata about a single
// large event so an operator (or host agent) can decide whether to fetch the
// full body, without the snapshot re-emitting that body into context. It
// never carries the full payload — first_line is whitespace-collapsed and
// rune-bounded, and retrieval_hint points at the explicit `traceary show`
// surface that intentionally bypasses the body cap.
type topSnapshotLargePayloadSample struct {
	EventID       string `json:"event_id"`
	Kind          string `json:"kind"`
	Source        string `json:"source"`
	MessageLength int    `json:"message_length"`
	MessageBytes  int    `json:"message_bytes"`
	FirstLine     string `json:"first_line,omitempty"`
	RetrievalHint string `json:"retrieval_hint"`
}

// topSnapshotNode is the JSON shape of a single node in the
// `traceary sessions --snapshot --json` / `traceary top --snapshot --json` output. It is intentionally
// independent from sessionTreeNode so the top contract can carry
// latest_event_* fields without reshaping the session tree contract
// that other consumers depend on.
//
// IsStale / StaleAfterSec / StaleAgeSec were added in v0.16.0 so script
// consumers can distinguish a fresh active session from one that has
// been abandoned beyond the configured stale threshold. The fields are
// optional — they only appear when the node is in the stale-active
// state, so existing tooling that does not yet read them is unaffected.
type topSnapshotNode struct {
	SessionID          string `json:"session_id"`
	ParentSessionID    string `json:"parent_session_id,omitempty"`
	SpawnEventID       string `json:"spawn_event_id,omitempty"`
	SubagentKind       string `json:"subagent_kind,omitempty"`
	SpawnOrder         *int   `json:"spawn_order,omitempty"`
	Depth              int    `json:"depth"`
	Workspace          string `json:"workspace,omitempty"`
	LatestEventKind    string `json:"latest_event_kind"`
	LatestEventID      string `json:"latest_event_id,omitempty"`
	LatestEventMessage string `json:"latest_event_message"`
	// LatestEventMessageTruncated / Length / Bytes are additive metadata
	// that appear only when the snapshot renderer cut latest_event_message
	// under the shared body cap. Consumers fetch the full body explicitly
	// with `traceary show <latest_event_id>`; legacy consumers that only
	// read latest_event_message keep working.
	LatestEventMessageTruncated bool `json:"latest_event_message_truncated,omitempty"`
	LatestEventMessageLength    int  `json:"latest_event_message_length,omitempty"`
	LatestEventMessageBytes     int  `json:"latest_event_message_bytes,omitempty"`

	LatestEventAt string             `json:"latest_event_at"`
	Label         string             `json:"label,omitempty"`
	Summary       string             `json:"summary,omitempty"`
	StartedAt     string             `json:"started_at"`
	EndedAt       *string            `json:"ended_at,omitempty"`
	Status        string             `json:"status"`
	DurationSec   *float64           `json:"duration_sec,omitempty"`
	TotalEvents   int                `json:"total_events"`
	CommandCount  int                `json:"command_count"`
	Agents        []string           `json:"agents"`
	SubagentType  string             `json:"subagent_type,omitempty"`
	IsStale       bool               `json:"is_stale,omitempty"`
	StaleAfterSec *float64           `json:"stale_after_seconds,omitempty"`
	StaleAgeSec   *float64           `json:"stale_age_seconds,omitempty"`
	Children      []*topSnapshotNode `json:"children"`
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

// staleMemoryOutput is the JSON shape of a stale durable-memory top row.
type staleMemoryOutput struct {
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
	Reason     string  `json:"reason"`
}

// memoryDetailsOutput is the JSON shape of a durable memory with refs.
type memoryDetailsOutput struct {
	Summary      memorySummaryOutput `json:"summary"`
	EvidenceRefs []string            `json:"evidence_refs"`
	ArtifactRefs []string            `json:"artifact_refs"`
}

// memoryDistillOutput is the JSON shape of a distillation run.
type memoryDistillOutput struct {
	Distilled memoryDetailsOutput   `json:"distilled"`
	Replace   string                `json:"replace"`
	Sources   []memorySummaryOutput `json:"sources"`
}

// sessionSummaryOutput is the JSON shape of a session summary in list output.
type sessionSummaryOutput struct {
	SessionID       string   `json:"session_id"`
	Workspace       string   `json:"workspace,omitempty"`
	Label           string   `json:"label,omitempty"`
	Summary         string   `json:"summary,omitempty"`
	Model           string   `json:"model,omitempty"`
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
	MemoryEdgesImported   int `json:"memory_edges_imported"`
	MemoryEdgesSkipped    int `json:"memory_edges_skipped"`
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
// low_quality_candidate_count keeps the number of candidate-side
// suggestions discoverable without walking Suggestions on the consumer
// side, mirroring the other counters. The field is documented in the
// hygiene scan tests so MCP/JSON consumers can rely on the new shape.
type memoryHygieneScanOutput struct {
	RedactionHitCount             int                        `json:"redaction_hit_count"`
	ExpiryCandidateCount          int                        `json:"expiry_candidate_count"`
	DuplicateCount                int                        `json:"duplicate_count"`
	SupersedeCandidateCount       int                        `json:"supersede_candidate_count"`
	ValidityOverlapSupersedeCount int                        `json:"validity_overlap_supersede_count"`
	LowQualityCandidateCount      int                        `json:"low_quality_candidate_count"`
	Suggestions                   []memoryHygieneOutputEntry `json:"suggestions"`
}

// memoryInboxBatchOutput is the JSON shape of a memory inbox batch result.
type memoryInboxBatchOutput struct {
	Action    string                `json:"action"`
	Processed []memoryDetailsOutput `json:"processed"`
	Failures  []memoryInboxFailure  `json:"failures,omitempty"`
}

// memoryInboxCleanupOutput is the JSON shape of an inbox cleanup preview/apply run.
type memoryInboxCleanupOutput struct {
	Action    string                    `json:"action"`
	DryRun    bool                      `json:"dry_run"`
	Summary   memoryInboxCleanupSummary `json:"summary"`
	Matched   []memoryDetailsOutput     `json:"matched,omitempty"`
	Processed []memoryDetailsOutput     `json:"processed,omitempty"`
	Failures  []memoryInboxFailure      `json:"failures,omitempty"`
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

// memoryActivationPlanOutput is the JSON shape of a dry-run activation plan.
//
// HostContext / ExternalMemory are populated only for two-file targets
// (Claude / Gemini); single-file Codex output omits them so existing
// JSON consumers see the same shape they did in v0.12.
type memoryActivationPlanOutput struct {
	Target         string                           `json:"target"`
	TargetPath     string                           `json:"target_path"`
	Existing       bool                             `json:"existing"`
	ActivatedCount int                              `json:"activated_count"`
	Markdown       string                           `json:"markdown"`
	Diff           string                           `json:"diff,omitempty"`
	HostContext    *memoryActivationComponentOutput `json:"host_context,omitempty"`
	ExternalMemory *memoryActivationComponentOutput `json:"external_memory,omitempty"`
}

// memoryActivationComponentOutput is the JSON shape of one file inside a
// two-file activation pair.
type memoryActivationComponentOutput struct {
	Path     string `json:"path"`
	Existing bool   `json:"existing"`
	Markdown string `json:"markdown,omitempty"`
	Diff     string `json:"diff,omitempty"`
	Action   string `json:"action,omitempty"`
	State    string `json:"state,omitempty"`
	Message  string `json:"message,omitempty"`
}

// memoryActivationApplyOutput is the JSON shape of a write activation result.
//
// HostContext / ExternalMemory are populated only for two-file targets
// (Claude / Gemini); single-file Codex output omits them so existing
// JSON consumers see the same shape they did in v0.12.
type memoryActivationApplyOutput struct {
	Target         string                           `json:"target"`
	TargetPath     string                           `json:"target_path"`
	Action         string                           `json:"action"`
	Existing       bool                             `json:"existing"`
	ActivatedCount int                              `json:"activated_count"`
	HostContext    *memoryActivationComponentOutput `json:"host_context,omitempty"`
	ExternalMemory *memoryActivationComponentOutput `json:"external_memory,omitempty"`
}

// memoryActivationStatusOutput is the JSON shape of a read-only activation status.
type memoryActivationStatusOutput struct {
	Target         string                           `json:"target"`
	TargetPath     string                           `json:"target_path"`
	State          string                           `json:"state"`
	Existing       bool                             `json:"existing"`
	ActivatedCount int                              `json:"activated_count"`
	Message        string                           `json:"message"`
	DryRunCommand  string                           `json:"dry_run_command,omitempty"`
	ApplyCommand   string                           `json:"apply_command,omitempty"`
	HostContext    *memoryActivationComponentOutput `json:"host_context,omitempty"`
	ExternalMemory *memoryActivationComponentOutput `json:"external_memory,omitempty"`
}

func formatJSONTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}
