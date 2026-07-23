package filesystem

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultCodexUsageMaxFileBytes int64 = 256 * 1024 * 1024
	defaultCodexUsageMaxLineBytes       = 8 * 1024 * 1024
)

type codexUsageSource struct {
	userHomeDir  func() (string, error)
	maxFileBytes int64
	maxLineBytes int
}

// NewCodexUsageSource creates the bounded local Codex rollout reader.
func NewCodexUsageSource() application.CodexUsageSource {
	return &codexUsageSource{
		userHomeDir:  osUserHomeDir,
		maxFileBytes: defaultCodexUsageMaxFileBytes,
		maxLineBytes: defaultCodexUsageMaxLineBytes,
	}
}

func newCodexUsageSourceWithHomeDir(userHomeDir func() (string, error)) *codexUsageSource {
	return &codexUsageSource{
		userHomeDir:  userHomeDir,
		maxFileBytes: defaultCodexUsageMaxFileBytes,
		maxLineBytes: defaultCodexUsageMaxLineBytes,
	}
}

func (s *codexUsageSource) Load(
	ctx context.Context,
	criteria application.CodexUsageLoadCriteria,
) (application.CodexUsageLoadResult, error) {
	if ctx == nil {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("context must not be nil")
	}
	sessionID := strings.TrimSpace(criteria.SessionID.String())
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex usage session ID")
	}
	root, err := s.resolveSessionsRoot()
	if err != nil {
		return application.CodexUsageLoadResult{}, err
	}
	paths, err := filepath.Glob(filepath.Join(root, "*", "*", "*", "*"+sessionID+".jsonl"))
	if err != nil {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("failed to match Codex usage source")
	}
	sort.Strings(paths)
	result := application.CodexUsageLoadResult{}
	for _, path := range paths {
		loaded, err := s.loadFile(ctx, root, path, sessionID)
		if err != nil {
			return application.CodexUsageLoadResult{}, err
		}
		result.Samples = append(result.Samples, loaded.Samples...)
		result.BoundaryObserved = loaded.BoundaryObserved
	}
	return result, nil
}

func (s *codexUsageSource) resolveSessionsRoot() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := s.userHomeDir()
		if err != nil {
			return "", xerrors.Errorf("failed to resolve Codex usage home")
		}
		codexHome = filepath.Join(home, ".codex")
	}
	root := filepath.Join(codexHome, "sessions")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return "", xerrors.Errorf("failed to resolve Codex sessions root")
	}
	return resolved, nil
}

func (s *codexUsageSource) loadFile(
	ctx context.Context,
	root string,
	path string,
	sessionID string,
) (application.CodexUsageLoadResult, error) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage source escapes sessions root")
	}
	info, err := validateCodexUsagePath(root, relative)
	if err != nil {
		return application.CodexUsageLoadResult{}, err
	}
	if info.Size() > s.maxFileBytes {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage source exceeds %d-byte limit", s.maxFileBytes)
	}
	file, err := os.Open(path) // #nosec G304 -- every path component was checked below the resolved sessions root
	if err != nil {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("failed to open Codex usage source")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage source changed during open")
	}
	if opened.Size() > s.maxFileBytes {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage source exceeds %d-byte limit", s.maxFileBytes)
	}
	limited := &io.LimitedReader{R: file, N: s.maxFileBytes + 1}
	fileName := filepath.Base(path)
	result, err := parseCodexRolloutUsageJSONL(
		ctx, limited, sessionID, codexUsageFileTime(fileName, opened.ModTime()), s.maxLineBytes,
	)
	if err != nil {
		return application.CodexUsageLoadResult{}, err
	}
	if limited.N == 0 {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage source exceeds %d-byte limit", s.maxFileBytes)
	}
	return result, nil
}

func validateCodexUsagePath(root, relative string) (os.FileInfo, error) {
	current := root
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, xerrors.Errorf("invalid Codex usage source path")
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return nil, xerrors.Errorf("failed to inspect Codex usage source")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, xerrors.Errorf("Codex usage source path must not contain symlinks")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, xerrors.Errorf("Codex usage source parent must be a directory")
		}
		if index == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return nil, xerrors.Errorf("Codex usage source must be a regular file")
			}
			return info, nil
		}
	}
	return nil, xerrors.Errorf("invalid Codex usage source path")
}

type codexUsageEnvelope struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type codexSessionMetaPayload struct {
	CLIVersion string `json:"cli_version"`
}

type codexTurnContextPayload struct {
	TurnID string `json:"turn_id"`
	Model  string `json:"model"`
}

type codexEventDiscriminator struct {
	Type string `json:"type"`
}

type codexTerminalPayload struct {
	TurnID string `json:"turn_id"`
}

type codexTokenCountPayload struct {
	Type string `json:"type"`
	Info struct {
		TotalTokenUsage *codexRawUsageCounters `json:"total_token_usage"`
	} `json:"info"`
}

type codexRawUsageCounters struct {
	InputTokens           *int64 `json:"input_tokens"`
	CachedInputTokens     *int64 `json:"cached_input_tokens"`
	CacheWriteInputTokens *int64 `json:"cache_write_input_tokens"`
	OutputTokens          *int64 `json:"output_tokens"`
	ReasoningOutputTokens *int64 `json:"reasoning_output_tokens"`
	TotalTokens           *int64 `json:"total_tokens"`
}

type codexRolloutSegment struct {
	turnID            string
	ordinal           int
	model             string
	baseline          codexRawUsageCounters
	baselineValid     bool
	counterRegression bool
	terminal          *codexRawUsageCounters
	terminalAt        time.Time
}

func parseCodexRolloutUsageJSONL(
	ctx context.Context,
	reader io.Reader,
	sessionID string,
	fallbackTime time.Time,
	maxLineBytes int,
) (application.CodexUsageLoadResult, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	version := "schema-v1"
	var previousTerminal *codexRawUsageCounters
	var lastSnapshot *codexRawUsageCounters
	continuityValid := true
	ordinal := 0
	var active *codexRolloutSegment
	result := application.CodexUsageLoadResult{}
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		select {
		case <-ctx.Done():
			return application.CodexUsageLoadResult{}, xerrors.Errorf("Codex usage read cancelled: %w", ctx.Err())
		default:
		}
		var envelope codexUsageEnvelope
		if err := json.Unmarshal(scanner.Bytes(), &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case "session_meta":
			var payload codexSessionMetaPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
				return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex session metadata at line %d", lineNumber)
			}
			if strings.TrimSpace(payload.CLIVersion) != "" {
				version = strings.TrimSpace(payload.CLIVersion)
			}
		case "turn_context":
			var payload codexTurnContextPayload
			if err := json.Unmarshal(envelope.Payload, &payload); err != nil || strings.TrimSpace(payload.TurnID) == "" {
				return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex turn context at line %d", lineNumber)
			}
			turnID := strings.TrimSpace(payload.TurnID)
			if active != nil && active.turnID == turnID {
				// Codex repeats the same turn context when a turn resumes after
				// compaction. It is the same cumulative segment, not a retry call.
				if model := strings.TrimSpace(payload.Model); model != "" {
					active.model = model
				}
				continue
			}
			ambiguousPrevious := active != nil
			if ambiguousPrevious {
				result.Samples = append(result.Samples, unavailableCodexRolloutSample(sessionID, version, *active, types.UsageTerminalUnknown, fallbackTime))
				previousTerminal = nil
				continuityValid = false
			}
			ordinal++
			baseline, valid := zeroCodexUsageCounters(), continuityValid && lastSnapshot == nil
			if previousTerminal != nil {
				baseline, valid = copyCodexUsageCounters(previousTerminal), continuityValid && completeCodexUsageCounters(previousTerminal)
			}
			active = &codexRolloutSegment{
				turnID: turnID, ordinal: ordinal, model: strings.TrimSpace(payload.Model),
				baseline: baseline, baselineValid: valid,
			}
			result.BoundaryObserved = false
		case "event_msg":
			var discriminator codexEventDiscriminator
			if err := json.Unmarshal(envelope.Payload, &discriminator); err != nil {
				return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex event discriminator at line %d", lineNumber)
			}
			switch discriminator.Type {
			case "token_count":
				var payload codexTokenCountPayload
				if err := json.Unmarshal(envelope.Payload, &payload); err != nil || payload.Info.TotalTokenUsage == nil || !completeCodexUsageCounters(payload.Info.TotalTokenUsage) {
					return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex token_count usage at line %d", lineNumber)
				}
				snapshot := copyCodexUsageCounters(payload.Info.TotalTokenUsage)
				regressed := false
				if lastSnapshot != nil {
					_, regressed = subtractCodexUsageCounters(snapshot, *lastSnapshot)
					regressed = !regressed
				}
				lastSnapshot = &snapshot
				if active == nil {
					previousTerminal = &snapshot
					continuityValid = true
					continue
				}
				observedAt, err := codexUsageTimestamp(envelope.Timestamp, fallbackTime)
				if err != nil {
					return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex token_count timestamp at line %d: %w", lineNumber, err)
				}
				active.terminal = &snapshot
				active.terminalAt = observedAt
				active.counterRegression = active.counterRegression || regressed
			case "task_complete", "turn_aborted":
				var payload codexTerminalPayload
				if err := json.Unmarshal(envelope.Payload, &payload); err != nil || strings.TrimSpace(payload.TurnID) == "" {
					return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex terminal boundary at line %d", lineNumber)
				}
				terminalCode := types.UsageTerminalSuccess
				if discriminator.Type == "turn_aborted" {
					terminalCode = types.UsageTerminalAbortedStream
				}
				if active == nil {
					ordinal++
					unmatched := codexRolloutSegment{turnID: strings.TrimSpace(payload.TurnID), ordinal: ordinal}
					result.Samples = append(result.Samples, unavailableCodexRolloutSample(sessionID, version, unmatched, terminalCode, observedAtOrFallback(envelope.Timestamp, fallbackTime)))
					previousTerminal = nil
					continuityValid = false
					result.BoundaryObserved = true
					continue
				}
				if active.turnID != strings.TrimSpace(payload.TurnID) {
					result.Samples = append(result.Samples, unavailableCodexRolloutSample(sessionID, version, *active, types.UsageTerminalUnknown, observedAtOrFallback(envelope.Timestamp, fallbackTime)))
					active = nil
					previousTerminal = nil
					continuityValid = false
					result.BoundaryObserved = false
					continue
				}
				observedAt, err := codexUsageTimestamp(envelope.Timestamp, active.terminalAt)
				if err != nil {
					return application.CodexUsageLoadResult{}, xerrors.Errorf("invalid Codex terminal timestamp at line %d: %w", lineNumber, err)
				}
				sample := finalizeCodexRolloutSample(sessionID, version, *active, terminalCode, observedAt)
				result.Samples = append(result.Samples, sample)
				if active.terminal != nil {
					snapshot := copyCodexUsageCounters(active.terminal)
					previousTerminal = &snapshot
					continuityValid = true
				} else {
					previousTerminal = nil
					continuityValid = false
				}
				active = nil
				result.BoundaryObserved = true
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return application.CodexUsageLoadResult{}, xerrors.Errorf("failed to scan Codex usage source")
	}
	if active != nil {
		result.BoundaryObserved = false
	}
	return result, nil
}

func observedAtOrFallback(value string, fallback time.Time) time.Time {
	observedAt, err := codexUsageTimestamp(value, fallback)
	if err != nil {
		return fallback.UTC()
	}
	return observedAt
}

func finalizeCodexRolloutSample(
	sessionID, version string,
	segment codexRolloutSegment,
	terminalCode types.UsageTerminalCode,
	observedAt time.Time,
) application.CodexUsageSample {
	if !segment.baselineValid || segment.counterRegression || segment.terminal == nil {
		return unavailableCodexRolloutSample(sessionID, version, segment, terminalCode, observedAt)
	}
	delta, ok := subtractCodexUsageCounters(*segment.terminal, segment.baseline)
	if !ok {
		return unavailableCodexRolloutSample(sessionID, version, segment, terminalCode, observedAt)
	}
	return application.CodexUsageSample{
		RecordID:      "rollout:" + sessionID + ":" + segment.turnID,
		SuppressionID: "headless_stream:" + sessionID + ":" + strconv.Itoa(segment.ordinal),
		SourceName:    "rollout_jsonl",
		SourceVersion: version,
		Model:         segment.model,
		ObservedAt:    observedAt.UTC(),
		TerminalCode:  terminalCode,
		Available:     true,
		Counters:      applicationCodexUsageCounters(delta),
	}
}

func unavailableCodexRolloutSample(
	sessionID, version string,
	segment codexRolloutSegment,
	terminalCode types.UsageTerminalCode,
	observedAt time.Time,
) application.CodexUsageSample {
	if observedAt.IsZero() {
		observedAt = segment.terminalAt
	}
	if observedAt.IsZero() {
		observedAt = time.Unix(0, 0).UTC()
	}
	return application.CodexUsageSample{
		RecordID:      "rollout:" + sessionID + ":" + segment.turnID,
		SuppressionID: "headless_stream:" + sessionID + ":" + strconv.Itoa(segment.ordinal),
		SourceName:    "rollout_jsonl",
		SourceVersion: version,
		Model:         segment.model,
		ObservedAt:    observedAt.UTC(),
		TerminalCode:  terminalCode,
		Available:     false,
	}
}

func completeCodexUsageCounters(value *codexRawUsageCounters) bool {
	if value == nil {
		return false
	}
	for _, counter := range []*int64{
		value.InputTokens, value.CachedInputTokens, value.CacheWriteInputTokens,
		value.OutputTokens, value.ReasoningOutputTokens, value.TotalTokens,
	} {
		if counter == nil || *counter < 0 {
			return false
		}
	}
	return true
}

func zeroCodexUsageCounters() codexRawUsageCounters {
	zero := int64(0)
	return codexRawUsageCounters{
		InputTokens: &zero, CachedInputTokens: &zero, CacheWriteInputTokens: &zero,
		OutputTokens: &zero, ReasoningOutputTokens: &zero, TotalTokens: &zero,
	}
}

func copyCodexUsageCounters(value *codexRawUsageCounters) codexRawUsageCounters {
	copyValue := func(input *int64) *int64 {
		if input == nil {
			return nil
		}
		value := *input
		return &value
	}
	return codexRawUsageCounters{
		InputTokens: copyValue(value.InputTokens), CachedInputTokens: copyValue(value.CachedInputTokens),
		CacheWriteInputTokens: copyValue(value.CacheWriteInputTokens), OutputTokens: copyValue(value.OutputTokens),
		ReasoningOutputTokens: copyValue(value.ReasoningOutputTokens), TotalTokens: copyValue(value.TotalTokens),
	}
}

func subtractCodexUsageCounters(terminal, baseline codexRawUsageCounters) (codexRawUsageCounters, bool) {
	difference := func(after, before *int64) (*int64, bool) {
		if after == nil || before == nil || *after < *before {
			return nil, false
		}
		value := *after - *before
		return &value, true
	}
	input, ok1 := difference(terminal.InputTokens, baseline.InputTokens)
	cached, ok2 := difference(terminal.CachedInputTokens, baseline.CachedInputTokens)
	cacheWrite, ok3 := difference(terminal.CacheWriteInputTokens, baseline.CacheWriteInputTokens)
	output, ok4 := difference(terminal.OutputTokens, baseline.OutputTokens)
	reasoning, ok5 := difference(terminal.ReasoningOutputTokens, baseline.ReasoningOutputTokens)
	total, ok6 := difference(terminal.TotalTokens, baseline.TotalTokens)
	return codexRawUsageCounters{
		InputTokens: input, CachedInputTokens: cached, CacheWriteInputTokens: cacheWrite,
		OutputTokens: output, ReasoningOutputTokens: reasoning, TotalTokens: total,
	}, ok1 && ok2 && ok3 && ok4 && ok5 && ok6
}

func applicationCodexUsageCounters(raw codexRawUsageCounters) application.CodexUsageCounters {
	return application.CodexUsageCounters{
		InputTokens: raw.InputTokens, CachedInputTokens: raw.CachedInputTokens,
		CacheWriteInputTokens: raw.CacheWriteInputTokens, OutputTokens: raw.OutputTokens,
		ReasoningOutputTokens: raw.ReasoningOutputTokens, TotalTokens: raw.TotalTokens,
	}
}

func codexUsageTimestamp(value string, fallback time.Time) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		if fallback.IsZero() {
			return time.Time{}, xerrors.Errorf("usage source has no timestamp")
		}
		return fallback.UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, trimmed)
	if err != nil {
		return time.Time{}, xerrors.Errorf("invalid usage timestamp")
	}
	return parsed.UTC(), nil
}

func codexUsageFileTime(fileName string, fallback time.Time) time.Time {
	const prefix = "rollout-"
	const encodedLength = len("2006-01-02T15-04-05")
	trimmed := strings.TrimPrefix(fileName, prefix)
	if len(trimmed) >= encodedLength {
		if parsed, err := time.Parse("2006-01-02T15-04-05", trimmed[:encodedLength]); err == nil {
			return parsed.UTC()
		}
	}
	return fallback.UTC()
}
