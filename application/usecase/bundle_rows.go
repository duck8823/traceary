// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// Portability covers all five tables — events, sessions, command_audits,
// memories, and memory_edges — see docs/operations/cross-machine-handoff
// for the operator guide.
package usecase

import (
	"sort"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func manifestTableEntries(
	manifest bundleManifest,
	registry map[string]bundleTableImporter,
) ([]bundleTableEntry, error) {
	switch manifest.ManifestVersion {
	case 1:
		const requiredChecksumFile = "events.ndjson"
		if _, ok := manifest.FileChecksums[requiredChecksumFile]; !ok {
			return nil, xerrors.Errorf(
				"bundle manifest is missing a checksum for %s (required for manifest_version=%d)",
				requiredChecksumFile, manifest.ManifestVersion,
			)
		}
		files := make([]string, 0, len(manifest.FileChecksums))
		for file := range manifest.FileChecksums {
			files = append(files, file)
		}
		sort.Strings(files)
		entries := make([]bundleTableEntry, 0, len(files))
		for _, file := range files {
			entry := bundleTableEntry{
				File:     file,
				Checksum: manifest.FileChecksums[file],
				RowCount: -1,
			}
			if file == requiredChecksumFile {
				entry.TableName = "events"
			}
			entries = append(entries, entry)
		}
		return entries, nil
	case 2:
		if len(manifest.Tables) == 0 {
			return nil, xerrors.Errorf("bundle manifest has no table entries")
		}
		for name := range manifest.Tables {
			if _, ok := registry[name]; !ok {
				return nil, xerrors.Errorf("bundle table %s is not supported by this build", name)
			}
		}
		names := bundleTableImportOrder()
		entries := make([]bundleTableEntry, 0, len(names))
		for _, name := range names {
			if _, present := manifest.Tables[name]; !present {
				continue
			}
			entry := manifest.Tables[name]
			if entry.TableName == "" {
				entry.TableName = name
			}
			if entry.TableName != name {
				return nil, xerrors.Errorf("bundle table key %s does not match table_name %s", name, entry.TableName)
			}
			if _, ok := registry[entry.TableName]; !ok {
				return nil, xerrors.Errorf("bundle table %s is not supported by this build", entry.TableName)
			}
			if entry.File == "" {
				return nil, xerrors.Errorf("bundle table %s has an empty file", entry.TableName)
			}
			if entry.Checksum == "" {
				return nil, xerrors.Errorf("bundle table %s has an empty checksum", entry.TableName)
			}
			if entry.RowCount < 0 {
				return nil, xerrors.Errorf("bundle table %s has a negative row_count", entry.TableName)
			}
			entries = append(entries, entry)
		}
		return entries, nil
	default:
		return nil, xerrors.Errorf("unsupported bundle manifest version %d", manifest.ManifestVersion)
	}
}

func verifyBundleFiles(files map[string][]byte, entries []bundleTableEntry) error {
	covered := map[string]bool{}
	for _, entry := range entries {
		data, present := files[entry.File]
		if !present {
			return xerrors.Errorf("bundle missing %s referenced by manifest", entry.File)
		}
		got := hashSHA256(data)
		if got != entry.Checksum {
			return xerrors.Errorf(
				"checksum mismatch on %s (want %s, got %s)", entry.File, entry.Checksum, got,
			)
		}
		covered[entry.File] = true
	}
	for name := range files {
		if name == "manifest.json" {
			continue
		}
		if !covered[name] {
			return xerrors.Errorf("bundle entry %s is not covered by a manifest checksum", name)
		}
	}
	return nil
}

type bundleEventRow struct {
	EventID    string `json:"event_id"`
	Kind       string `json:"kind"`
	Client     string `json:"client"`
	Agent      string `json:"agent"`
	SessionID  string `json:"session_id"`
	Workspace  string `json:"workspace"`
	Body       string `json:"body"`
	SourceHook string `json:"source_hook,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type bundleSessionRow struct {
	SessionID       string `json:"session_id"`
	StartedAt       string `json:"started_at"`
	EndedAt         string `json:"ended_at,omitempty"`
	Client          string `json:"client"`
	Agent           string `json:"agent"`
	Workspace       string `json:"workspace"`
	Label           string `json:"label,omitempty"`
	Summary         string `json:"summary,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	SpawnEventID    string `json:"spawn_event_id,omitempty"`
	SubagentKind    string `json:"subagent_kind,omitempty"`
	SpawnOrder      *int   `json:"spawn_order,omitempty"`
	RuntimeMode     string `json:"runtime_mode,omitempty"`
	TerminalReason  string `json:"terminal_reason,omitempty"`
}

type bundleCommandAuditRow struct {
	EventID             string `json:"event_id"`
	Command             string `json:"command"`
	Input               string `json:"input"`
	Output              string `json:"output"`
	InputTruncated      bool   `json:"input_truncated"`
	OutputTruncated     bool   `json:"output_truncated"`
	InputOriginalBytes  int    `json:"input_original_bytes,omitempty"`
	OutputOriginalBytes int    `json:"output_original_bytes,omitempty"`
	ExitCode            *int   `json:"exit_code,omitempty"`
	Failed              bool   `json:"failed,omitempty"`
}

type bundleRefRow struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type bundleMemoryRow struct {
	MemoryID           string         `json:"memory_id"`
	Type               string         `json:"type"`
	ScopeKind          string         `json:"scope_kind"`
	ScopeValue         string         `json:"scope_value"`
	Fact               string         `json:"fact"`
	Status             string         `json:"status"`
	Confidence         string         `json:"confidence"`
	Source             string         `json:"source"`
	EvidenceRefs       []bundleRefRow `json:"evidence_refs,omitempty"`
	ArtifactRefs       []bundleRefRow `json:"artifact_refs,omitempty"`
	SupersedesMemoryID string         `json:"supersedes_memory_id,omitempty"`
	ExpiresAt          string         `json:"expires_at,omitempty"`
	ValidFrom          string         `json:"valid_from"`
	ValidTo            string         `json:"valid_to,omitempty"`
	CreatedAt          string         `json:"created_at"`
	UpdatedAt          string         `json:"updated_at"`
}

type bundleMemoryEdgeRow struct {
	EdgeID       string `json:"id"`
	FromMemoryID string `json:"from_memory_id"`
	ToMemoryID   string `json:"to_memory_id"`
	RelationType string `json:"relation_type"`
	ValidFrom    string `json:"valid_from"`
	ValidTo      string `json:"valid_to,omitempty"`
	CreatedAt    string `json:"created_at"`
}

func (r bundleMemoryEdgeRow) toMemoryEdge() (*model.MemoryEdge, error) {
	edgeID, err := types.MemoryEdgeIDFrom(r.EdgeID)
	if err != nil {
		return nil, xerrors.Errorf("id: %w", err)
	}
	fromID, err := types.MemoryIDFrom(r.FromMemoryID)
	if err != nil {
		return nil, xerrors.Errorf("from_memory_id: %w", err)
	}
	toID, err := types.MemoryIDFrom(r.ToMemoryID)
	if err != nil {
		return nil, xerrors.Errorf("to_memory_id: %w", err)
	}
	validFrom, err := time.Parse(time.RFC3339Nano, r.ValidFrom)
	if err != nil {
		return nil, xerrors.Errorf("valid_from: %w", err)
	}
	validTo, err := parseOptionalBundleTime(r.ValidTo, "valid_to")
	if err != nil {
		return nil, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, xerrors.Errorf("created_at: %w", err)
	}
	edge, err := model.NewMemoryEdge(edgeID, fromID, toID, types.MemoryEdgeRelationOf(r.RelationType), validFrom, validTo, createdAt)
	if err != nil {
		return nil, xerrors.Errorf("memory edge: %w", err)
	}
	return edge, nil
}

func (r bundleMemoryRow) toMemory() (*model.Memory, error) {
	memoryID, err := types.MemoryIDFrom(r.MemoryID)
	if err != nil {
		return nil, xerrors.Errorf("memory_id: %w", err)
	}
	memoryType, err := types.MemoryTypeFrom(r.Type)
	if err != nil {
		return nil, xerrors.Errorf("type: %w", err)
	}
	scope, err := types.MemoryScopeFrom(r.ScopeKind, r.ScopeValue)
	if err != nil {
		return nil, xerrors.Errorf("scope: %w", err)
	}
	// Bundle imports intentionally do not trust source lifecycle state:
	// every newly imported memory enters the existing review inbox first.
	status := types.MemoryStatusCandidate
	confidence, err := types.ConfidenceFrom(r.Confidence)
	if err != nil {
		return nil, xerrors.Errorf("confidence: %w", err)
	}
	source, err := types.MemorySourceFrom(r.Source)
	if err != nil {
		return nil, xerrors.Errorf("source: %w", err)
	}
	evidenceRefs := make([]types.EvidenceRef, 0, len(r.EvidenceRefs))
	for _, ref := range r.EvidenceRefs {
		kind, err := types.EvidenceRefKindFrom(ref.Kind)
		if err != nil {
			return nil, xerrors.Errorf("evidence ref kind: %w", err)
		}
		restored, err := types.EvidenceRefFrom(kind, ref.Value)
		if err != nil {
			return nil, xerrors.Errorf("evidence ref: %w", err)
		}
		evidenceRefs = append(evidenceRefs, restored)
	}
	artifactRefs := make([]types.ArtifactRef, 0, len(r.ArtifactRefs))
	for _, ref := range r.ArtifactRefs {
		kind, err := types.ArtifactRefKindFrom(ref.Kind)
		if err != nil {
			return nil, xerrors.Errorf("artifact ref kind: %w", err)
		}
		restored, err := types.ArtifactRefFrom(kind, ref.Value)
		if err != nil {
			return nil, xerrors.Errorf("artifact ref: %w", err)
		}
		artifactRefs = append(artifactRefs, restored)
	}
	supersedes := types.None[types.MemoryID]()
	if r.SupersedesMemoryID != "" {
		supersededID, err := types.MemoryIDFrom(r.SupersedesMemoryID)
		if err != nil {
			return nil, xerrors.Errorf("supersedes_memory_id: %w", err)
		}
		supersedes = types.Some(supersededID)
	}
	expiresAt, err := parseOptionalBundleTime(r.ExpiresAt, "expires_at")
	if err != nil {
		return nil, err
	}
	validFrom, err := time.Parse(time.RFC3339Nano, r.ValidFrom)
	if err != nil {
		return nil, xerrors.Errorf("valid_from: %w", err)
	}
	validTo, err := parseOptionalBundleTime(r.ValidTo, "valid_to")
	if err != nil {
		return nil, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, xerrors.Errorf("created_at: %w", err)
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, r.UpdatedAt)
	if err != nil {
		return nil, xerrors.Errorf("updated_at: %w", err)
	}
	return model.MemoryOf(memoryID, memoryType, scope, r.Fact, status, confidence, source, evidenceRefs, artifactRefs, supersedes, expiresAt, validFrom, validTo, createdAt, updatedAt), nil
}

func (r bundleSessionRow) toSession() (*model.Session, error) {
	sessionID, err := types.SessionIDFrom(r.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session_id: %w", err)
	}
	startedAt, err := time.Parse(time.RFC3339Nano, r.StartedAt)
	if err != nil {
		return nil, xerrors.Errorf("started_at: %w", err)
	}
	endedAt, err := parseOptionalBundleTime(r.EndedAt, "ended_at")
	if err != nil {
		return nil, err
	}
	agent, err := types.AgentFrom(r.Agent)
	if err != nil {
		return nil, xerrors.Errorf("agent: %w", err)
	}
	spawnOrder := types.None[int]()
	if r.SpawnOrder != nil {
		spawnOrder = types.Some(*r.SpawnOrder)
	}
	runtimeMode := types.RuntimeMode("")
	if r.RuntimeMode != "" {
		runtimeMode, err = types.RuntimeModeFrom(r.RuntimeMode)
		if err != nil {
			return nil, xerrors.Errorf("runtime_mode: %w", err)
		}
	}
	terminalReason := types.None[types.TerminalReason]()
	if r.TerminalReason != "" {
		reason, err := types.TerminalReasonFrom(r.TerminalReason)
		if err != nil {
			return nil, xerrors.Errorf("terminal_reason: %w", err)
		}
		terminalReason = types.Some(reason)
	}
	restored, err := model.SessionFromSnapshot(model.SessionSnapshot{
		SessionID:       sessionID,
		StartedAt:       startedAt,
		EndedAt:         endedAt,
		Client:          types.Client(r.Client),
		Agent:           agent,
		Workspace:       types.Workspace(r.Workspace),
		Label:           r.Label,
		Summary:         r.Summary,
		RuntimeMode:     runtimeMode,
		TerminalReason:  terminalReason,
		ParentSessionID: types.SessionID(r.ParentSessionID),
		SpawnEventID:    types.EventID(r.SpawnEventID),
		SubagentKind:    r.SubagentKind,
		SpawnOrder:      spawnOrder,
	})
	if err != nil {
		return nil, xerrors.Errorf("session lifecycle: %w", err)
	}
	return restored, nil
}

func (r bundleCommandAuditRow) toCommandAudit() (*model.CommandAudit, error) {
	eventID, err := types.EventIDFrom(r.EventID)
	if err != nil {
		return nil, xerrors.Errorf("event_id: %w", err)
	}
	exitCode := types.None[int]()
	if r.ExitCode != nil {
		exitCode = types.Some(*r.ExitCode)
	}
	audit := model.CommandAuditOf(
		eventID,
		r.Command,
		r.Input,
		r.Output,
		r.InputTruncated,
		r.OutputTruncated,
		exitCode,
		r.Failed,
	)
	audit.SetOriginalPayloadBytes(r.InputOriginalBytes, r.OutputOriginalBytes)
	return audit, nil
}

func (r bundleEventRow) toEvent() (*model.Event, error) {
	eventID, err := types.EventIDFrom(r.EventID)
	if err != nil {
		return nil, xerrors.Errorf("event_id: %w", err)
	}
	kind, err := types.EventKindFrom(r.Kind)
	if err != nil {
		return nil, xerrors.Errorf("kind: %w", err)
	}
	agent, err := types.AgentFrom(r.Agent)
	if err != nil {
		return nil, xerrors.Errorf("agent: %w", err)
	}
	sessionID, err := types.SessionIDFrom(r.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session_id: %w", err)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, r.CreatedAt)
	if err != nil {
		return nil, xerrors.Errorf("created_at: %w", err)
	}
	return model.EventOfWithSourceHook(
		eventID, kind,
		types.Client(r.Client), agent, sessionID,
		types.Workspace(r.Workspace),
		r.Body, createdAt, r.SourceHook,
	), nil
}
