package mcpserver

import apptypes "github.com/duck8823/traceary/application/types"

// recordEventOutput is the uniform MCP output for record_event type=log|audit.
type recordEventOutput struct {
	EventID             string                    `json:"event_id" jsonschema:"saved event ID"`
	Type                string                    `json:"type" jsonschema:"event write type: log or audit"`
	Kind                string                    `json:"kind" jsonschema:"event kind"`
	Client              string                    `json:"client" jsonschema:"recording channel"`
	Agent               string                    `json:"agent" jsonschema:"actor"`
	SessionID           string                    `json:"session_id" jsonschema:"session identifier"`
	Workspace           string                    `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body                string                    `json:"body,omitempty" jsonschema:"event body plain-text projection"`
	BodyBlocks          []apptypes.EventBodyBlock `json:"body_blocks,omitempty" jsonschema:"structured block form of transcript body"`
	Command             string                    `json:"command,omitempty" jsonschema:"executed command for audit records"`
	Wrapper             string                    `json:"wrapper,omitempty" jsonschema:"verified command wrapper when present"`
	CommandName         string                    `json:"command_name,omitempty" jsonschema:"normalized executable used for aggregation"`
	ExitCode            *int                      `json:"exit_code,omitempty" jsonschema:"captured command exit code"`
	Failed              bool                      `json:"failed,omitempty" jsonschema:"whether structured outcome evidence indicates failure"`
	FailureReason       string                    `json:"failure_reason,omitempty" jsonschema:"structured command outcome reason"`
	InputRedacted       bool                      `json:"input_redacted" jsonschema:"whether input was redacted"`
	OutputRedacted      bool                      `json:"output_redacted" jsonschema:"whether output was redacted"`
	InputTruncated      bool                      `json:"input_truncated" jsonschema:"whether input was truncated"`
	OutputTruncated     bool                      `json:"output_truncated" jsonschema:"whether output was truncated"`
	InputOriginalBytes  int                       `json:"input_original_bytes,omitempty" jsonschema:"original input byte count when input_truncated is true and known"`
	OutputOriginalBytes int                       `json:"output_original_bytes,omitempty" jsonschema:"original output byte count when output_truncated is true and known"`
	SourceHook          string                    `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event"`
	CreatedAt           string                    `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// sessionEventOutput is the MCP output for session tools (start/end/active/latest).
type sessionEventOutput struct {
	EventID    string `json:"event_id" jsonschema:"saved or referenced event ID"`
	Kind       string `json:"kind" jsonschema:"event kind"`
	Client     string `json:"client" jsonschema:"recording channel"`
	Agent      string `json:"agent" jsonschema:"actor"`
	SessionID  string `json:"session_id" jsonschema:"session identifier"`
	Workspace  string `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	SourceHook string `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event (omitted for non-hook writes)"`
	CreatedAt  string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// eventsOutput is the MCP output for event listing tools (list_events, search, get_context).
type eventsOutput struct {
	Events   []eventOutput   `json:"events" jsonschema:"events matching the filters"`
	Interval *intervalOutput `json:"interval,omitempty" jsonschema:"requested and effective half-open interval used by list_events or search"`
}

type intervalOutput struct {
	RequestedFrom          string `json:"requested_from" jsonschema:"caller-supplied lower bound"`
	RequestedTo            string `json:"requested_to" jsonschema:"caller-supplied upper bound"`
	EffectiveFromInclusive string `json:"effective_from_inclusive" jsonschema:"resolved inclusive UTC lower bound"`
	EffectiveToExclusive   string `json:"effective_to_exclusive" jsonschema:"resolved exclusive UTC upper bound"`
	Timezone               string `json:"timezone" jsonschema:"IANA timezone used for date-only bounds"`
	SnapshotAt             string `json:"snapshot_at" jsonschema:"UTC snapshot used for omitted upper bounds"`
}

// eventOutput is an individual event in an eventsOutput list.
type eventOutput struct {
	EventID               string                    `json:"event_id" jsonschema:"event ID"`
	Kind                  string                    `json:"kind" jsonschema:"event kind"`
	Client                string                    `json:"client" jsonschema:"recording channel"`
	Agent                 string                    `json:"agent" jsonschema:"actor"`
	SessionID             string                    `json:"session_id" jsonschema:"session identifier"`
	Workspace             string                    `json:"workspace,omitempty" jsonschema:"auxiliary work context identifier"`
	Body                  *string                   `json:"body,omitempty" jsonschema:"event body as a plain-text projection; absent for projection=metadata. For transcript JSON envelopes this joins text blocks and excludes thinking blocks — use body_blocks for the canonical structured form. May be truncated to body_limit characters; check body_truncated and body_length"`
	BodyUnavailableReason string                    `json:"body_unavailable_reason,omitempty" jsonschema:"reason the raw body is unavailable; currently retention"`
	BodyBlocks            []apptypes.EventBodyBlock `json:"body_blocks,omitempty" jsonschema:"structured block form of the body when it is a canonical transcript envelope; populated for list_events only — search and get_context omit it so thinking-block text does not leak through those surfaces; also absent for legacy plain-text bodies, non-envelope JSON bodies, empty envelopes, and rows whose body is truncated"`
	BodyTruncated         bool                      `json:"body_truncated,omitempty" jsonschema:"true when body was truncated to fit body_limit. Re-issue the same call with full_body=true (or a larger body_limit) to disable response truncation; audit payloads truncated at ingestion stay visibly truncated and are not recoverable"`
	BodyLength            int                       `json:"body_length,omitempty" jsonschema:"original body length in runes before any truncation; only emitted when body_truncated is true"`
	BodyOriginalBytes     *int                      `json:"body_original_bytes,omitempty" jsonschema:"original event body byte count when known"`
	BodyStoredBytes       *int                      `json:"body_stored_bytes,omitempty" jsonschema:"stored event body byte count; available for projection=metadata"`
	BodyIngestTruncated   *bool                     `json:"body_ingest_truncated,omitempty" jsonschema:"whether ingestion truncated the event body when known"`
	BodyStorageTruncated  *bool                     `json:"body_storage_truncated,omitempty" jsonschema:"whether storage policy truncated the event body when known"`
	ExitCode              *int                      `json:"exit_code,omitempty" jsonschema:"command exit code when available in metadata projection"`
	Failed                bool                      `json:"failed,omitempty" jsonschema:"whether the linked command audit failed"`
	SourceHook            string                    `json:"source_hook,omitempty" jsonschema:"hook identifier that produced this event (omitted for non-hook writes)"`
	CreatedAt             string                    `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
}

// sessionHandoffOutput is the MCP output for the session_handoff tool.
type sessionHandoffOutput struct {
	SessionID             string                `json:"session_id,omitempty"`
	Workspace             string                `json:"workspace,omitempty"`
	RequestedWorkspace    string                `json:"requested_workspace,omitempty" jsonschema:"workspace originally requested by the caller when it differs from the matched session workspace"`
	WorkspaceFallbackUsed bool                  `json:"workspace_fallback_used,omitempty" jsonschema:"true when Traceary matched a parent workspace session using event evidence under the requested workspace"`
	WorkspaceMatchNote    string                `json:"workspace_match_note,omitempty" jsonschema:"human-readable explanation of parent-workspace fallback"`
	Label                 string                `json:"label,omitempty"`
	Status                string                `json:"status,omitempty"`
	TotalEvents           int                   `json:"total_events"`
	CommandCount          int                   `json:"command_count"`
	Agents                []string              `json:"agents,omitempty"`
	Summary               string                `json:"summary,omitempty" jsonschema:"legacy compatibility mirror of working_state.combined_summary"`
	WorkingState          workingStateOutput    `json:"working_state"`
	RecentCommands        []string              `json:"recent_commands,omitempty"`
	RecentCommandItems    []recentCommandOutput `json:"recent_command_items,omitempty" jsonschema:"structured body-safe recent commands with extent and truncation provenance"`
	Memories              []memorySummaryOutput `json:"memories,omitempty"`
	MemoryNeedsReview     []memorySummaryOutput `json:"memory_needs_review,omitempty" jsonschema:"candidate memories included only when include_candidates is true; review before trusting"`
	AcceptedMemoryCount   int                   `json:"accepted_memory_count" jsonschema:"number of accepted memories loaded into trusted context"`
	CandidateMemoryCount  int                   `json:"candidate_memory_count" jsonschema:"number of candidate memories observed under the context-pack limit"`
}

type recentCommandOutput struct {
	EventID               string `json:"event_id" jsonschema:"event identity for explicit detail retrieval"`
	Summary               string `json:"summary" jsonschema:"body-safe single-line command summary"`
	BodyOriginalBytes     *int   `json:"body_original_bytes,omitempty" jsonschema:"original payload bytes when known"`
	BodyStoredBytes       int    `json:"body_stored_bytes" jsonschema:"persisted payload bytes"`
	BodyReturnedBytes     int    `json:"body_returned_bytes" jsonschema:"summary bytes returned by this response"`
	BodyResponseTruncated bool   `json:"body_response_truncated" jsonschema:"whether this response omitted persisted body content"`
	BodyIngestTruncated   *bool  `json:"body_ingest_truncated,omitempty" jsonschema:"whether ingestion truncated the original payload when known"`
	BodyStorageTruncated  *bool  `json:"body_storage_truncated,omitempty" jsonschema:"whether storage policy truncated the ingested payload when known"`
	CreatedAt             string `json:"created_at" jsonschema:"event timestamp (RFC3339Nano)"`
	RetrievalHint         string `json:"retrieval_hint" jsonschema:"explicit command for retrieving full stored event detail"`
}

// sessionLineageOutput is the MCP output for session_status action=lineage.
type sessionLineageOutput struct {
	Roots []sessionLineageNodeOutput `json:"roots" jsonschema:"top-level root nodes in the lineage tree"`
}

type sessionLineageNodeOutput struct {
	SessionID       string                     `json:"session_id" jsonschema:"session identifier"`
	ParentSessionID string                     `json:"parent_session_id,omitempty" jsonschema:"parent session identifier"`
	SpawnEventID    string                     `json:"spawn_event_id,omitempty" jsonschema:"event that spawned this child session"`
	SubagentKind    string                     `json:"subagent_kind,omitempty" jsonschema:"subagent spawn kind"`
	SpawnOrder      *int                       `json:"spawn_order,omitempty" jsonschema:"sibling spawn order"`
	Depth           int                        `json:"depth" jsonschema:"depth from the lineage root"`
	Workspace       string                     `json:"workspace,omitempty" jsonschema:"workspace identifier"`
	Label           string                     `json:"label,omitempty" jsonschema:"user-assigned session label"`
	Summary         string                     `json:"summary,omitempty" jsonschema:"session summary"`
	StartedAt       string                     `json:"started_at" jsonschema:"start timestamp"`
	EndedAt         *string                    `json:"ended_at,omitempty" jsonschema:"end timestamp"`
	Status          string                     `json:"status" jsonschema:"session status"`
	DurationSec     *float64                   `json:"duration_sec,omitempty" jsonschema:"duration in seconds"`
	TotalEvents     int                        `json:"total_events" jsonschema:"total event count"`
	CommandCount    int                        `json:"command_count" jsonschema:"command event count"`
	Agents          []string                   `json:"agents" jsonschema:"agents seen in this session"`
	SubagentType    string                     `json:"subagent_type,omitempty" jsonschema:"most specific subagent role"`
	Children        []sessionLineageNodeOutput `json:"children" jsonschema:"child sessions ordered by spawn_order"`
}

// memoryPackOutput is the MCP output for the memory_pack tool.
type memoryPackOutput = sessionHandoffOutput

// workingStateOutput represents structured working-memory signals.
type workingStateOutput struct {
	SessionSummary  string `json:"session_summary,omitempty"`
	CompactSummary  string `json:"compact_summary,omitempty"`
	CombinedSummary string `json:"combined_summary,omitempty"`
}

// memoriesOutput is the MCP output for retrieve_memories.
type memoriesOutput struct {
	Memories []memoryOutput `json:"memories" jsonschema:"durable memories matching the request"`
}

// memorySummaryOutput is the compact durable-memory shape used inside context packs.
//
// Optional timestamps are rendered as `*string` + `omitempty`: an absent
// bound produces JSON `null` / an omitted key rather than an empty
// string. This matches the CLI shape (presentation/cli/output.go
// memorySummaryOutput) so consumers of memory_pack and `memory list
// --json` can share the same TypeScript / JSON Schema shape.
type memorySummaryOutput struct {
	MemoryID   string  `json:"memory_id" jsonschema:"durable memory identifier"`
	Type       string  `json:"type" jsonschema:"memory type"`
	ScopeKind  string  `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue string  `json:"scope_value" jsonschema:"scope value"`
	Fact       string  `json:"fact" jsonschema:"distilled memory fact"`
	Status     string  `json:"status" jsonschema:"lifecycle status"`
	Confidence string  `json:"confidence" jsonschema:"confidence level"`
	Source     string  `json:"source" jsonschema:"memory source"`
	Supersedes *string `json:"supersedes,omitempty" jsonschema:"superseded memory identifier; null or omitted when this memory does not supersede another"`
	ExpiresAt  *string `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano); null or omitted when unset"`
	ValidFrom  string  `json:"valid_from" jsonschema:"start of content validity window (RFC3339Nano)"`
	ValidTo    *string `json:"valid_to,omitempty" jsonschema:"end of content validity window (RFC3339Nano); null or omitted means open-ended"`
	CreatedAt  string  `json:"created_at" jsonschema:"creation timestamp (RFC3339Nano)"`
	UpdatedAt  string  `json:"updated_at" jsonschema:"update timestamp (RFC3339Nano)"`
}

// memoryOutput is the full durable-memory shape returned by MCP memory
// tools. Shape matches presentation/cli/output.go memorySummaryOutput
// so a consumer can share a single JSON / TypeScript definition for
// both `memory list --json` and the MCP retrieve_memories tool — see
// #628.
type memoryOutput struct {
	MemoryID     string            `json:"memory_id" jsonschema:"durable memory identifier"`
	Type         string            `json:"type" jsonschema:"memory type"`
	ScopeKind    string            `json:"scope_kind" jsonschema:"scope kind"`
	ScopeValue   string            `json:"scope_value" jsonschema:"scope value"`
	Fact         string            `json:"fact" jsonschema:"distilled memory fact"`
	Status       string            `json:"status" jsonschema:"lifecycle status"`
	Confidence   string            `json:"confidence" jsonschema:"confidence level"`
	Source       string            `json:"source" jsonschema:"memory source"`
	Supersedes   *string           `json:"supersedes,omitempty" jsonschema:"superseded memory identifier; null or omitted when this memory does not supersede another"`
	ExpiresAt    *string           `json:"expires_at,omitempty" jsonschema:"expiry timestamp (RFC3339Nano); null or omitted when unset"`
	ValidFrom    string            `json:"valid_from" jsonschema:"start of content validity window (RFC3339Nano)"`
	ValidTo      *string           `json:"valid_to,omitempty" jsonschema:"end of content validity window (RFC3339Nano); null or omitted means open-ended"`
	CreatedAt    string            `json:"created_at" jsonschema:"creation timestamp (RFC3339Nano)"`
	UpdatedAt    string            `json:"updated_at" jsonschema:"update timestamp (RFC3339Nano)"`
	EvidenceRefs []memoryRefOutput `json:"evidence_refs,omitempty" jsonschema:"supporting evidence refs"`
	ArtifactRefs []memoryRefOutput `json:"artifact_refs,omitempty" jsonschema:"related artifact refs"`
}

// memoryRefOutput is a reference rendered in MCP responses.
type memoryRefOutput struct {
	Kind  string `json:"kind" jsonschema:"reference kind"`
	Value string `json:"value" jsonschema:"reference value"`
}

// inboxBatchMemoryOutput mirrors the CLI `memory inbox` batch summary so
// agent hosts get the same action / success / failure breakdown from MCP
// as an operator sees on stdout. Failures are keyed by the raw id the
// caller supplied so retries can target only the memories that did not
// transition.
type inboxBatchMemoryOutput struct {
	Action    string                          `json:"action" jsonschema:"batch action that was applied (accept or reject)"`
	Processed []memoryOutput                  `json:"processed" jsonschema:"memories that transitioned successfully"`
	Failures  []inboxBatchMemoryFailureOutput `json:"failures,omitempty" jsonschema:"memories that failed to transition"`
}

type inboxBatchMemoryFailureOutput struct {
	MemoryID string `json:"memory_id" jsonschema:"requested memory identifier"`
	Error    string `json:"error" jsonschema:"reason the memory did not transition"`
}

// exportMemoriesOutput mirrors the CLI summary of a memory export run so
// MCP consumers receive the generated markdown plus the same counters
// operators see on stdout.
type exportMemoriesOutput struct {
	Target        string `json:"target" jsonschema:"target host that was exported"`
	ExportedCount int    `json:"exported_count" jsonschema:"number of accepted memories included in the markdown block"`
	Markdown      string `json:"markdown" jsonschema:"generated markdown block wrapped in Traceary marker comments"`
}

// importMemoryInstructionsOutput mirrors the CLI `memory import
// instructions` shape so every import surface reports the same
// imported / skipped / warnings breakdown.
type importMemoryInstructionsOutput struct {
	Imported              []memoryOutput `json:"imported" jsonschema:"memories that were proposed as new candidates"`
	SkippedDuplicateCount int            `json:"skipped_duplicate_count" jsonschema:"bullets that matched an existing candidate / accepted memory"`
	SkippedRejectedCount  int            `json:"skipped_rejected_count" jsonschema:"bullets that matched a memory the operator already rejected"`
	Warnings              []string       `json:"warnings,omitempty" jsonschema:"non-fatal parser or sanitizer notes"`
}

// memoryHygieneOutput mirrors the CLI `memory hygiene scan` shape so
// agent hosts receive the same six-suggestion view an operator sees on
// stdout. low_quality_candidate_count is the v0.12 candidate-noise
// counter (#864) and is documented in tests as part of the JSON shape.
type memoryHygieneOutput struct {
	RedactionHitCount             int                             `json:"redaction_hit_count" jsonschema:"number of accepted memories flagged by the current redaction rules"`
	ExpiryCandidateCount          int                             `json:"expiry_candidate_count" jsonschema:"number of accepted memories older than the staleness threshold"`
	DuplicateCount                int                             `json:"duplicate_count" jsonschema:"number of accepted memories that share scope + fact with another row"`
	SupersedeCandidateCount       int                             `json:"supersede_candidate_count" jsonschema:"number of accepted memories paired by word-Jaccard similarity"`
	ValidityOverlapSupersedeCount int                             `json:"validity_overlap_supersede_count" jsonschema:"number of accepted memories paired by (scope, type) with overlapping validity windows"`
	LowQualityCandidateCount      int                             `json:"low_quality_candidate_count" jsonschema:"number of candidate memories flagged by the deterministic low-quality classifier"`
	Suggestions                   []memoryHygieneSuggestionOutput `json:"suggestions" jsonschema:"per-memory hygiene suggestions"`
}

type memoryHygieneSuggestionOutput struct {
	MemoryID            string   `json:"memory_id" jsonschema:"memory identifier"`
	Kind                string   `json:"kind" jsonschema:"suggestion kind (redaction_hit / expiry_candidate / duplicate / supersede_candidate / validity_overlap_supersede / low_quality_candidate)"`
	Reason              string   `json:"reason" jsonschema:"human-readable reason the scanner flagged this memory"`
	Fact                string   `json:"fact" jsonschema:"stored fact at scan time"`
	SanitizedFact       string   `json:"sanitized_fact,omitempty" jsonschema:"masked fact the apply path would write via supersede"`
	DuplicateMemoryID   string   `json:"duplicate_memory_id,omitempty" jsonschema:"paired duplicate when kind=duplicate"`
	ReplacementMemoryID string   `json:"replacement_memory_id,omitempty" jsonschema:"newer memory whose fact is suggested as the supersede replacement"`
	ReplacementFact     string   `json:"replacement_fact,omitempty" jsonschema:"fact the apply path would write via supersede"`
	Similarity          float64  `json:"similarity,omitempty" jsonschema:"word-Jaccard similarity score (supersede_candidate / validity_overlap_supersede only)"`
	ScopeKind           string   `json:"scope_kind,omitempty" jsonschema:"scope kind"`
	ScopeValue          string   `json:"scope_value,omitempty" jsonschema:"scope value"`
	UpdatedAt           string   `json:"updated_at" jsonschema:"last update timestamp (RFC3339)"`
	Status              string   `json:"status,omitempty" jsonschema:"memory lifecycle status (only populated for candidate-side suggestions)"`
	Source              string   `json:"source,omitempty" jsonschema:"memory source (only populated for candidate-side suggestions)"`
	QualityReasons      []string `json:"quality_reasons,omitempty" jsonschema:"deterministic noise markers from the extraction-quality classifier (low_quality_candidate only)"`
}
