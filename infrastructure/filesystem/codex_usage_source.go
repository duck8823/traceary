package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
		return application.CodexUsageLoadResult{}, xerrors.Errorf("failed to match Codex usage source: %w", err)
	}
	sort.Strings(paths)
	result := application.CodexUsageLoadResult{}
	for _, path := range paths {
		samples, err := s.loadFile(ctx, root, path)
		if err != nil {
			return application.CodexUsageLoadResult{}, err
		}
		result.Samples = append(result.Samples, samples...)
	}
	return result, nil
}

func (s *codexUsageSource) resolveSessionsRoot() (string, error) {
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		home, err := s.userHomeDir()
		if err != nil {
			return "", xerrors.Errorf("failed to resolve Codex usage home: %w", err)
		}
		codexHome = filepath.Join(home, ".codex")
	}
	root := filepath.Join(codexHome, "sessions")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return "", xerrors.Errorf("failed to resolve Codex sessions root: %w", err)
	}
	return resolved, nil
}

func (s *codexUsageSource) loadFile(
	ctx context.Context,
	root string,
	path string,
) ([]application.CodexUsageSample, error) {
	contained, err := filepath.Rel(root, path)
	if err != nil || contained == ".." || strings.HasPrefix(contained, ".."+string(filepath.Separator)) {
		return nil, xerrors.Errorf("Codex usage source escapes sessions root")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, xerrors.Errorf("failed to inspect Codex usage source: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, xerrors.Errorf("Codex usage source must be a regular non-symlink file")
	}
	if info.Size() > s.maxFileBytes {
		return nil, xerrors.Errorf("Codex usage source exceeds %d-byte limit", s.maxFileBytes)
	}
	file, err := os.Open(path) // #nosec G304 -- fixed-depth match under the resolved Codex sessions root
	if err != nil {
		return nil, xerrors.Errorf("failed to open Codex usage source: %w", err)
	}
	defer func() { _ = file.Close() }()
	fileName := filepath.Base(path)
	return parseCodexUsageJSONL(ctx, file, fileName, codexUsageFileTime(fileName, info.ModTime()), s.maxLineBytes)
}

type codexUsageEnvelope struct {
	Timestamp string                 `json:"timestamp"`
	Type      string                 `json:"type"`
	Payload   json.RawMessage        `json:"payload"`
	Usage     *codexRawUsageCounters `json:"usage"`
	Model     string                 `json:"model"`
}

type codexSessionMetaPayload struct {
	CLIVersion string `json:"cli_version"`
}

type codexTurnContextPayload struct {
	Model string `json:"model"`
}

type codexEventPayload struct {
	Type string `json:"type"`
	Info *struct {
		LastTokenUsage *codexRawUsageCounters `json:"last_token_usage"`
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

func parseCodexUsageJSONL(
	ctx context.Context,
	reader io.Reader,
	fileName string,
	fallbackTime time.Time,
	maxLineBytes int,
) ([]application.CodexUsageSample, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	fileID := codexUsageFileID(fileName)
	version := "schema-v1"
	modelName := ""
	samples := make([]application.CodexUsageSample, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		select {
		case <-ctx.Done():
			return nil, xerrors.Errorf("Codex usage read cancelled: %w", ctx.Err())
		default:
		}
		line := scanner.Bytes()
		var envelope codexUsageEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			// Rollout files can contain future non-usage rows. A malformed row
			// cannot be treated as authoritative usage, so ignore it rather than
			// exposing or persisting its body.
			continue
		}
		switch envelope.Type {
		case "session_meta":
			var payload codexSessionMetaPayload
			if json.Unmarshal(envelope.Payload, &payload) == nil && strings.TrimSpace(payload.CLIVersion) != "" {
				version = strings.TrimSpace(payload.CLIVersion)
			}
		case "turn_context":
			var payload codexTurnContextPayload
			if json.Unmarshal(envelope.Payload, &payload) == nil {
				modelName = strings.TrimSpace(payload.Model)
			}
		case "event_msg":
			var payload codexEventPayload
			if json.Unmarshal(envelope.Payload, &payload) != nil || payload.Type != "token_count" || payload.Info == nil || payload.Info.LastTokenUsage == nil {
				continue
			}
			if isCodexCompactionCarryover(payload.Info.LastTokenUsage) {
				continue
			}
			observedAt, err := codexUsageTimestamp(envelope.Timestamp, fallbackTime)
			if err != nil {
				return nil, xerrors.Errorf("invalid Codex token_count timestamp at line %d: %w", lineNumber, err)
			}
			samples = append(samples, codexUsageSample(fileID, lineNumber, "rollout_jsonl", version, modelName, observedAt, payload.Info.LastTokenUsage))
		case "turn.completed":
			if envelope.Usage == nil {
				continue
			}
			observedAt, err := codexUsageTimestamp(envelope.Timestamp, fallbackTime)
			if err != nil {
				return nil, xerrors.Errorf("invalid Codex turn.completed timestamp at line %d: %w", lineNumber, err)
			}
			directModel := strings.TrimSpace(envelope.Model)
			if directModel == "" {
				directModel = modelName
			}
			samples = append(samples, codexUsageSample(fileID, lineNumber, "exec_jsonl", version, directModel, observedAt, envelope.Usage))
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, xerrors.Errorf("failed to scan Codex usage source: %w", err)
	}
	return samples, nil
}

func isCodexCompactionCarryover(usage *codexRawUsageCounters) bool {
	if usage == nil || usage.TotalTokens == nil || *usage.TotalTokens <= 0 {
		return false
	}
	for _, value := range []*int64{
		usage.InputTokens, usage.CachedInputTokens, usage.CacheWriteInputTokens,
		usage.OutputTokens, usage.ReasoningOutputTokens,
	} {
		if value == nil || *value != 0 {
			return false
		}
	}
	return true
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
		return time.Time{}, xerrors.Errorf("invalid usage timestamp: %w", err)
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

func codexUsageSample(
	fileID string,
	lineNumber int,
	sourceName string,
	version string,
	modelName string,
	observedAt time.Time,
	raw *codexRawUsageCounters,
) application.CodexUsageSample {
	return application.CodexUsageSample{
		RecordID:      sourceName + ":" + fileID + ":" + strconv.Itoa(lineNumber),
		SourceName:    sourceName,
		SourceVersion: version,
		Model:         modelName,
		ObservedAt:    observedAt,
		Counters: application.CodexUsageCounters{
			InputTokens: raw.InputTokens, CachedInputTokens: raw.CachedInputTokens,
			CacheWriteInputTokens: raw.CacheWriteInputTokens, OutputTokens: raw.OutputTokens,
			ReasoningOutputTokens: raw.ReasoningOutputTokens, TotalTokens: raw.TotalTokens,
		},
	}
}

func codexUsageFileID(fileName string) string {
	digest := sha256.Sum256([]byte(fileName))
	return hex.EncodeToString(digest[:8])
}
