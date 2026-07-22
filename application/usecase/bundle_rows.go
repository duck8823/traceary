// Package usecase — bundle usecase implements the v0.9 portability
// primitive introduced for #572: a local-first, encrypted,
// content-verifiable archive that operators can move between their
// machines through any file-transport they already have (AirDrop,
// scp, Syncthing, etc.). Traceary never ships its own transport.
//
// Portability covers events, sessions, command_audits, memories, memory_edges,
// and usage_observations — see docs/operations/cross-machine-handoff
// for the operator guide.
package usecase

import (
	"sort"
	"strings"
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
	Wrapper             string `json:"wrapper,omitempty"`
	CommandName         string `json:"command_name"`
	Input               string `json:"input"`
	Output              string `json:"output"`
	InputTruncated      bool   `json:"input_truncated"`
	OutputTruncated     bool   `json:"output_truncated"`
	InputOriginalBytes  int    `json:"input_original_bytes,omitempty"`
	OutputOriginalBytes int    `json:"output_original_bytes,omitempty"`
	ExitCode            *int   `json:"exit_code,omitempty"`
	Failed              bool   `json:"failed,omitempty"`
	FailureReason       string `json:"failure_reason"`
}

type bundleUsageObservationRow struct {
	ObservationID         string `json:"observation_id"`
	SessionID             string `json:"session_id"`
	Host                  string `json:"host"`
	SourceName            string `json:"source_name"`
	SourceVersion         string `json:"source_version"`
	Provider              string `json:"provider,omitempty"`
	Model                 string `json:"model,omitempty"`
	Scope                 string `json:"scope"`
	Accounting            string `json:"accounting"`
	Status                string `json:"status"`
	ObservedAt            string `json:"observed_at"`
	FinalizedAt           string `json:"finalized_at,omitempty"`
	TerminalCode          string `json:"terminal_code,omitempty"`
	InputState            string `json:"input_state"`
	InputTokens           *int64 `json:"input_tokens,omitempty"`
	CachedInputState      string `json:"cached_input_state"`
	CachedInputTokens     *int64 `json:"cached_input_tokens,omitempty"`
	CacheWriteInputState  string `json:"cache_write_input_state"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens,omitempty"`
	OutputState           string `json:"output_state"`
	OutputTokens          *int64 `json:"output_tokens,omitempty"`
	ReasoningOutputState  string `json:"reasoning_output_state"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens,omitempty"`
	TotalState            string `json:"total_state"`
	TotalTokens           *int64 `json:"total_tokens,omitempty"`
	CostState             string `json:"cost_state"`
	CostAmountMicros      *int64 `json:"cost_amount_micros,omitempty"`
	CostCurrency          string `json:"cost_currency,omitempty"`
	CostOrigin            string `json:"cost_origin,omitempty"`
	PriceTableVersion     string `json:"price_table_version,omitempty"`
	SnapshotSeries        string `json:"snapshot_series,omitempty"`
	SnapshotRevision      *int64 `json:"snapshot_revision,omitempty"`
	SupersedesID          string `json:"supersedes_id,omitempty"`
	RunHost               string `json:"run_host,omitempty"`
	RunID                 string `json:"run_id,omitempty"`
}

func bundleUsageObservationRowFromModel(observation *model.UsageObservation) bundleUsageObservationRow {
	descriptor := observation.Descriptor()
	source := descriptor.Source()
	counters := observation.Counters()
	cost := observation.Cost()
	row := bundleUsageObservationRow{
		ObservationID: descriptor.ObservationID().String(), SessionID: descriptor.SessionID().String(),
		Host: source.Host(), SourceName: source.Name(), SourceVersion: source.Version(),
		Provider: source.Provider(), Model: source.Model(), Scope: descriptor.Scope().String(),
		Accounting: descriptor.Accounting().String(), Status: observation.Status().String(),
		ObservedAt: descriptor.ObservedAt().UTC().Format(time.RFC3339Nano),
		InputState: counters.Input().State().String(), InputTokens: bundleUsageValuePointer(counters.Input()),
		CachedInputState: counters.CachedInput().State().String(), CachedInputTokens: bundleUsageValuePointer(counters.CachedInput()),
		CacheWriteInputState: counters.CacheWriteInput().State().String(), CacheWriteInputTokens: bundleUsageValuePointer(counters.CacheWriteInput()),
		OutputState: counters.Output().State().String(), OutputTokens: bundleUsageValuePointer(counters.Output()),
		ReasoningOutputState: counters.ReasoningOutput().State().String(), ReasoningOutputTokens: bundleUsageValuePointer(counters.ReasoningOutput()),
		TotalState: counters.Total().State().String(), TotalTokens: bundleUsageValuePointer(counters.Total()),
		CostState: cost.State().String(), CostCurrency: cost.Currency(), CostOrigin: cost.Origin().String(),
		PriceTableVersion: cost.PriceTableVersion(), SnapshotSeries: descriptor.SnapshotSeries(),
	}
	if value, present := cost.AmountMicros(); present {
		row.CostAmountMicros = &value
	}
	if value, present := observation.FinalizedAt().Value(); present {
		row.FinalizedAt = value.UTC().Format(time.RFC3339Nano)
	}
	if value, present := observation.TerminalCode().Value(); present {
		row.TerminalCode = value.String()
	}
	if descriptor.SnapshotRevision() > 0 {
		value := descriptor.SnapshotRevision()
		row.SnapshotRevision = &value
	}
	if value, present := descriptor.SupersedesID().Value(); present {
		row.SupersedesID = value.String()
	}
	if value, present := descriptor.RunIdentity().Value(); present {
		row.RunHost = value.Host()
		row.RunID = value.RunID()
	}
	return row
}

func bundleUsageValuePointer(value types.UsageValue) *int64 {
	if numeric, present := value.Value(); present {
		return &numeric
	}
	return nil
}

func (r bundleUsageObservationRow) toUsageObservation() (*model.UsageObservation, error) {
	id, err := types.UsageObservationIDFrom(r.ObservationID)
	if err != nil {
		return nil, xerrors.Errorf("observation_id: %w", err)
	}
	sessionID, err := types.SessionIDFrom(r.SessionID)
	if err != nil {
		return nil, xerrors.Errorf("session_id: %w", err)
	}
	source, err := types.UsageSourceOf(r.Host, r.SourceName, r.SourceVersion, r.Provider, r.Model)
	if err != nil {
		return nil, xerrors.Errorf("source: %w", err)
	}
	scope, err := types.UsageScopeFrom(r.Scope)
	if err != nil {
		return nil, xerrors.Errorf("scope: %w", err)
	}
	accounting, err := types.UsageAccountingFrom(r.Accounting)
	if err != nil {
		return nil, xerrors.Errorf("accounting: %w", err)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, r.ObservedAt)
	if err != nil {
		return nil, xerrors.Errorf("observed_at: %w", err)
	}
	var descriptor model.UsageObservationDescriptor
	if scope == types.UsageScopeSessionSnapshot {
		if r.SnapshotRevision == nil {
			return nil, xerrors.Errorf("snapshot_revision is required for a session snapshot")
		}
		predecessor := types.None[types.UsageObservationID]()
		if r.SupersedesID != "" {
			value, err := types.UsageObservationIDFrom(r.SupersedesID)
			if err != nil {
				return nil, xerrors.Errorf("supersedes_id: %w", err)
			}
			predecessor = types.Some(value)
		}
		descriptor, err = model.NewUsageSnapshotDescriptor(id, sessionID, source, r.SnapshotSeries, *r.SnapshotRevision, predecessor, observedAt)
	} else if r.RunHost == "" && r.RunID == "" {
		descriptor, err = model.NewUsageObservationDescriptor(id, sessionID, source, scope, accounting, observedAt)
	} else if r.RunHost == "" || r.RunID == "" {
		return nil, xerrors.Errorf("run identity is incomplete")
	} else {
		runIdentity, identityErr := types.RunIdentityFrom(r.RunHost, r.RunID)
		if identityErr != nil {
			return nil, xerrors.Errorf("run identity: %w", identityErr)
		}
		descriptor, err = model.NewUsageObservationDescriptorWithRunIdentity(id, sessionID, source, scope, accounting, observedAt, runIdentity)
	}
	if err != nil {
		return nil, xerrors.Errorf("descriptor: %w", err)
	}
	values := []struct {
		name  string
		state string
		value *int64
	}{
		{"input", r.InputState, r.InputTokens}, {"cached_input", r.CachedInputState, r.CachedInputTokens},
		{"cache_write_input", r.CacheWriteInputState, r.CacheWriteInputTokens}, {"output", r.OutputState, r.OutputTokens},
		{"reasoning_output", r.ReasoningOutputState, r.ReasoningOutputTokens}, {"total", r.TotalState, r.TotalTokens},
	}
	restored := make([]types.UsageValue, 0, len(values))
	for _, item := range values {
		optional := types.None[int64]()
		if item.value != nil {
			optional = types.Some(*item.value)
		}
		value, err := types.UsageValueFrom(item.state, optional)
		if err != nil {
			return nil, xerrors.Errorf("%s usage: %w", item.name, err)
		}
		restored = append(restored, value)
	}
	counters, err := types.UsageCountersOf(restored[0], restored[1], restored[2], restored[3], restored[4], restored[5])
	if err != nil {
		return nil, xerrors.Errorf("counters: %w", err)
	}
	costAmount := types.None[int64]()
	if r.CostAmountMicros != nil {
		costAmount = types.Some(*r.CostAmountMicros)
	}
	cost, err := types.UsageCostFrom(r.CostState, costAmount, r.CostCurrency, r.CostOrigin, r.PriceTableVersion)
	if err != nil {
		return nil, xerrors.Errorf("cost: %w", err)
	}
	status, err := types.UsageObservationStatusFrom(r.Status)
	if err != nil {
		return nil, xerrors.Errorf("status: %w", err)
	}
	terminal := types.None[types.UsageTerminalCode]()
	if r.TerminalCode != "" {
		value, err := types.UsageTerminalCodeFrom(r.TerminalCode)
		if err != nil {
			return nil, xerrors.Errorf("terminal_code: %w", err)
		}
		terminal = types.Some(value)
	}
	finalizedAt := types.None[time.Time]()
	if r.FinalizedAt != "" {
		value, err := time.Parse(time.RFC3339Nano, r.FinalizedAt)
		if err != nil {
			return nil, xerrors.Errorf("finalized_at: %w", err)
		}
		finalizedAt = types.Some(value)
	}
	observation, err := model.UsageObservationOf(descriptor, status, counters, cost, terminal, finalizedAt)
	if err != nil {
		return nil, xerrors.Errorf("aggregate: %w", err)
	}
	return observation, nil
}

type bundleRunLineageRow struct {
	Host              string  `json:"host"`
	RunID             string  `json:"run_id"`
	ParentHost        *string `json:"parent_host,omitempty"`
	ParentRunID       *string `json:"parent_run_id,omitempty"`
	SessionID         *string `json:"session_id,omitempty"`
	BatchID           *string `json:"batch_id,omitempty"`
	TicketRef         *string `json:"ticket_ref,omitempty"`
	Repository        *string `json:"repository,omitempty"`
	PullRequestNumber *int64  `json:"pull_request_number,omitempty"`
	HeadSHA           *string `json:"head_sha,omitempty"`
	PacketSHA256      *string `json:"packet_sha256,omitempty"`
	PacketBytes       *int64  `json:"packet_bytes,omitempty"`
	ToolOutputBytes   *int64  `json:"tool_output_bytes,omitempty"`
}

func bundleRunLineageRowFromModel(lineage *model.RunLineage) bundleRunLineageRow {
	identity := lineage.Identity()
	row := bundleRunLineageRow{Host: identity.Host(), RunID: identity.RunID()}
	if value, present := lineage.Parent().Value(); present {
		host, runID := value.Host(), value.RunID()
		row.ParentHost, row.ParentRunID = &host, &runID
	}
	if value, present := lineage.SessionID().Value(); present {
		sessionID := value.String()
		row.SessionID = &sessionID
	}
	work := lineage.Work()
	if value, present := work.BatchID().Value(); present {
		row.BatchID = &value
	}
	if value, present := work.TicketRef().Value(); present {
		row.TicketRef = &value
	}
	if value, present := work.Repository().Value(); present {
		row.Repository = &value
	}
	if value, present := work.PullRequestNumber().Value(); present {
		row.PullRequestNumber = &value
	}
	if value, present := work.HeadSHA().Value(); present {
		row.HeadSHA = &value
	}
	if value, present := lineage.Packet().Value(); present {
		bytes := value.Bytes()
		sha := value.SHA256()
		row.PacketSHA256, row.PacketBytes = &sha, &bytes
	}
	if value, present := lineage.ToolOutputBytes().Value(); present {
		row.ToolOutputBytes = &value
	}
	return row
}

func (r bundleRunLineageRow) toRunLineage() (*model.RunLineage, error) {
	identity, err := types.RunIdentityFrom(r.Host, r.RunID)
	if err != nil {
		return nil, xerrors.Errorf("identity: %w", err)
	}
	parent := types.None[types.RunIdentity]()
	if r.ParentHost != nil || r.ParentRunID != nil {
		if r.ParentHost == nil || r.ParentRunID == nil {
			return nil, xerrors.Errorf("parent identity is incomplete")
		}
		value, err := types.RunIdentityFrom(*r.ParentHost, *r.ParentRunID)
		if err != nil {
			return nil, xerrors.Errorf("parent identity: %w", err)
		}
		parent = types.Some(value)
	}
	session := types.None[types.SessionID]()
	if r.SessionID != nil {
		value, err := types.SessionIDFrom(*r.SessionID)
		if err != nil {
			return nil, xerrors.Errorf("session_id: %w", err)
		}
		session = types.Some(value)
	}
	work, err := types.RunWorkAttributionFrom(optionalBundleString(r.BatchID), optionalBundleString(r.TicketRef), optionalBundleString(r.Repository), optionalBundleInt64(r.PullRequestNumber), optionalBundleString(r.HeadSHA))
	if err != nil {
		return nil, xerrors.Errorf("work attribution: %w", err)
	}
	packet := types.None[types.PacketIdentity]()
	if r.PacketSHA256 != nil || r.PacketBytes != nil {
		if r.PacketSHA256 == nil || r.PacketBytes == nil {
			return nil, xerrors.Errorf("packet identity is incomplete")
		}
		value, err := types.PacketIdentityFrom(*r.PacketSHA256, *r.PacketBytes)
		if err != nil {
			return nil, xerrors.Errorf("packet identity: %w", err)
		}
		packet = types.Some(value)
	}
	lineage, err := model.RunLineageOf(identity, parent, session, work, packet, optionalBundleInt64(r.ToolOutputBytes))
	if err != nil {
		return nil, xerrors.Errorf("lineage: %w", err)
	}
	return lineage, nil
}

func optionalBundleString(value *string) types.Optional[string] {
	if value == nil {
		return types.None[string]()
	}
	return types.Some(*value)
}
func optionalBundleInt64(value *int64) types.Optional[int64] {
	if value == nil {
		return types.None[int64]()
	}
	return types.Some(*value)
}

func sortBundleRunLineageRows(rows []bundleRow) ([]bundleRunLineageRow, error) {
	byIdentity := make(map[types.RunIdentity]bundleRunLineageRow, len(rows))
	for _, generic := range rows {
		row, ok := generic.(bundleRunLineageRow)
		if !ok {
			return nil, xerrors.Errorf("unexpected run_lineages row type %T", generic)
		}
		lineage, err := row.toRunLineage()
		if err != nil {
			return nil, err
		}
		if _, exists := byIdentity[lineage.Identity()]; exists {
			return nil, xerrors.Errorf("duplicate run lineage identity")
		}
		byIdentity[lineage.Identity()] = row
	}
	result := make([]bundleRunLineageRow, 0, len(rows))
	state := make(map[types.RunIdentity]uint8, len(rows))
	var visit func(types.RunIdentity) error
	visit = func(identity types.RunIdentity) error {
		switch state[identity] {
		case 1:
			return xerrors.Errorf("run lineage graph contains a cycle")
		case 2:
			return nil
		}
		row, exists := byIdentity[identity]
		if !exists {
			return xerrors.Errorf("run lineage parent is missing")
		}
		state[identity] = 1
		if row.ParentHost != nil {
			parent, err := types.RunIdentityFrom(*row.ParentHost, *row.ParentRunID)
			if err != nil {
				return xerrors.Errorf("invalid parent run identity: %w", err)
			}
			if err := visit(parent); err != nil {
				return err
			}
		}
		state[identity] = 2
		result = append(result, row)
		return nil
	}
	identities := make([]types.RunIdentity, 0, len(byIdentity))
	for identity := range byIdentity {
		identities = append(identities, identity)
	}
	sort.Slice(identities, func(i, j int) bool {
		if identities[i].Host() != identities[j].Host() {
			return identities[i].Host() < identities[j].Host()
		}
		return identities[i].RunID() < identities[j].RunID()
	})
	for _, identity := range identities {
		if err := visit(identity); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func sortBundleUsageObservationRows(rows []bundleRow) ([]bundleUsageObservationRow, error) {
	observations := make([]bundleUsageObservationRow, 0, len(rows))
	for _, generic := range rows {
		row, ok := generic.(bundleUsageObservationRow)
		if !ok {
			return nil, xerrors.Errorf("unexpected usage_observations row type %T", generic)
		}
		observations = append(observations, row)
	}
	sort.Slice(observations, func(i, j int) bool {
		if observations[i].SnapshotSeries != observations[j].SnapshotSeries {
			return observations[i].SnapshotSeries < observations[j].SnapshotSeries
		}
		leftRevision := int64(0)
		rightRevision := int64(0)
		if observations[i].SnapshotRevision != nil {
			leftRevision = *observations[i].SnapshotRevision
		}
		if observations[j].SnapshotRevision != nil {
			rightRevision = *observations[j].SnapshotRevision
		}
		if leftRevision != rightRevision {
			return leftRevision < rightRevision
		}
		return observations[i].ObservationID < observations[j].ObservationID
	})
	return observations, nil
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
	wrapper := types.None[types.CommandName]()
	if strings.TrimSpace(r.Wrapper) != "" {
		wrapper = types.Some(types.CommandName(r.Wrapper))
	}
	audit, err := model.CommandAuditFromSnapshot(model.CommandAuditSnapshot{
		EventID: eventID, Command: r.Command, Wrapper: wrapper,
		CommandName: types.CommandName(r.CommandName), Input: r.Input, Output: r.Output,
		InputTruncated: r.InputTruncated, OutputTruncated: r.OutputTruncated,
		InputOriginalBytes: r.InputOriginalBytes, OutputOriginalBytes: r.OutputOriginalBytes,
		ExitCode: exitCode, Failed: r.Failed, FailureReason: types.CommandFailureReason(r.FailureReason),
	})
	if err != nil {
		return nil, xerrors.Errorf("command audit: %w", err)
	}
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
