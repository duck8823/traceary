package filesystem

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

const (
	defaultClaudeUsageMaxFileBytes int64 = 256 * 1024 * 1024
	defaultClaudeUsageMaxLineBytes       = 8 * 1024 * 1024
)

type claudeUsageSource struct {
	userHomeDir  func() (string, error)
	maxFileBytes int64
	maxLineBytes int
}

// NewClaudeUsageSource creates the bounded local Claude transcript reader.
func NewClaudeUsageSource() application.ClaudeUsageSource {
	return &claudeUsageSource{
		userHomeDir:  osUserHomeDir,
		maxFileBytes: defaultClaudeUsageMaxFileBytes,
		maxLineBytes: defaultClaudeUsageMaxLineBytes,
	}
}

func newClaudeUsageSourceWithHomeDir(userHomeDir func() (string, error)) *claudeUsageSource {
	return &claudeUsageSource{
		userHomeDir:  userHomeDir,
		maxFileBytes: defaultClaudeUsageMaxFileBytes,
		maxLineBytes: defaultClaudeUsageMaxLineBytes,
	}
}

func (s *claudeUsageSource) Load(
	ctx context.Context,
	criteria application.ClaudeUsageLoadCriteria,
) (application.ClaudeUsageLoadResult, error) {
	if ctx == nil {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("context must not be nil")
	}
	sessionID := strings.TrimSpace(criteria.SessionID.String())
	if sessionID == "" || filepath.Base(sessionID) != sessionID || strings.ContainsAny(sessionID, `/\\`) {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("invalid Claude usage session ID")
	}
	root, err := s.resolveProjectsRoot()
	if err != nil {
		return application.ClaudeUsageLoadResult{}, err
	}
	rootHandle, err := os.OpenRoot(root)
	if err != nil {
		if os.IsNotExist(err) {
			return application.ClaudeUsageLoadResult{Mode: application.ClaudeUsageModeTranscriptCalls}, nil
		}
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("failed to open Claude projects root")
	}
	defer func() { _ = rootHandle.Close() }()
	entries, err := fs.ReadDir(rootHandle.FS(), ".")
	if err != nil {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("failed to list Claude projects root")
	}
	paths := make([]string, 0, 1)
	for _, entry := range entries {
		if entry.Type()&fs.ModeSymlink != 0 {
			return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
				"Claude projects root must not contain symlinked project directories",
			)
		}
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(entry.Name(), sessionID+".jsonl")
		if _, err := rootHandle.Lstat(candidate); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
				"failed to inspect Claude usage source",
			)
		}
		paths = append(paths, candidate)
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return application.ClaudeUsageLoadResult{Mode: application.ClaudeUsageModeTranscriptCalls}, nil
	}
	if len(paths) != 1 {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("ambiguous Claude usage source")
	}
	return s.loadFile(ctx, rootHandle, paths[0], sessionID)
}

func (s *claudeUsageSource) resolveProjectsRoot() (string, error) {
	claudeHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeHome == "" {
		home, err := s.userHomeDir()
		if err != nil {
			return "", xerrors.Errorf("failed to resolve Claude usage home")
		}
		claudeHome = filepath.Join(home, ".claude")
	}
	root := filepath.Join(claudeHome, "projects")
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		if os.IsNotExist(err) {
			return root, nil
		}
		return "", xerrors.Errorf("failed to resolve Claude projects root")
	}
	return resolved, nil
}

func (s *claudeUsageSource) loadFile(
	ctx context.Context,
	root *os.Root,
	relative string,
	sessionID string,
) (application.ClaudeUsageLoadResult, error) {
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(relative) {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("Claude usage source escapes projects root")
	}
	info, err := validateClaudeUsagePath(root, relative)
	if err != nil {
		return application.ClaudeUsageLoadResult{}, err
	}
	if info.Size() > s.maxFileBytes {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
			"Claude usage source exceeds %d-byte limit", s.maxFileBytes,
		)
	}
	file, err := root.Open(relative)
	if err != nil {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("failed to open Claude usage source")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("Claude usage source changed during open")
	}
	limited := &io.LimitedReader{R: file, N: s.maxFileBytes + 1}
	result, err := parseClaudeUsageJSONL(ctx, limited, sessionID, s.maxLineBytes)
	if err != nil {
		return application.ClaudeUsageLoadResult{}, err
	}
	if limited.N == 0 {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
			"Claude usage source exceeds %d-byte limit", s.maxFileBytes,
		)
	}
	return result, nil
}

func validateClaudeUsagePath(root *os.Root, relative string) (os.FileInfo, error) {
	current := ""
	parts := strings.Split(filepath.Clean(relative), string(filepath.Separator))
	for index, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, xerrors.Errorf("invalid Claude usage source path")
		}
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return nil, xerrors.Errorf("failed to inspect Claude usage source")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, xerrors.Errorf("Claude usage source path must not contain symlinks")
		}
		if index < len(parts)-1 && !info.IsDir() {
			return nil, xerrors.Errorf("Claude usage source parent must be a directory")
		}
		if index == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return nil, xerrors.Errorf("Claude usage source must be a regular file")
			}
			return info, nil
		}
	}
	return nil, xerrors.Errorf("invalid Claude usage source path")
}

type claudeUsageEnvelope struct {
	Type       string          `json:"type"`
	Timestamp  string          `json:"timestamp"`
	SessionID  string          `json:"session_id"`
	SessionID2 string          `json:"sessionId"`
	RequestID  string          `json:"requestId"`
	Message    json.RawMessage `json:"message"`
	Usage      json.RawMessage `json:"usage"`
	ModelUsage json.RawMessage `json:"modelUsage"`
	Subtype    string          `json:"subtype"`
	IsError    bool            `json:"is_error"`
}

type claudeAssistantMessage struct {
	ID    string          `json:"id"`
	Model string          `json:"model"`
	Usage json.RawMessage `json:"usage"`
}

type claudeRawUsageCounters struct {
	InputTokens         *int64 `json:"input_tokens"`
	CacheReadTokens     *int64 `json:"cache_read_input_tokens"`
	CacheCreationTokens *int64 `json:"cache_creation_input_tokens"`
	OutputTokens        *int64 `json:"output_tokens"`
}

func parseClaudeUsageJSONL(
	ctx context.Context,
	reader io.Reader,
	sessionID string,
	maxLineBytes int,
) (application.ClaudeUsageLoadResult, error) {
	result := application.ClaudeUsageLoadResult{Mode: application.ClaudeUsageModeTranscriptCalls}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxLineBytes)
	calls := make([]application.ClaudeUsageSample, 0)
	seenCalls := make(map[string]application.ClaudeUsageSample)
	var terminal *application.ClaudeUsageSample
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return application.ClaudeUsageLoadResult{}, xerrors.Errorf("Claude usage read canceled: %w", err)
		}
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var envelope claudeUsageEnvelope
		if err := json.Unmarshal(line, &envelope); err != nil {
			return application.ClaudeUsageLoadResult{}, xerrors.Errorf("invalid Claude usage JSON event")
		}
		rowSessionID := strings.TrimSpace(envelope.SessionID)
		if rowSessionID == "" {
			rowSessionID = strings.TrimSpace(envelope.SessionID2)
		}
		if rowSessionID != "" && rowSessionID != sessionID {
			continue
		}
		switch envelope.Type {
		case "assistant":
			sample, identity, relevant, err := claudeAssistantUsageSample(envelope, sessionID)
			if err != nil {
				return application.ClaudeUsageLoadResult{}, err
			}
			if !relevant {
				continue
			}
			if existing, found := seenCalls[identity]; found {
				if !sameClaudeUsageSample(existing, sample) {
					return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
						"conflicting duplicate Claude assistant usage",
					)
				}
				continue
			}
			seenCalls[identity] = sample
			calls = append(calls, sample)
			result.BoundaryObserved = true
		case "result":
			sample, err := claudeResultUsageSample(envelope, sessionID)
			if err != nil {
				return application.ClaudeUsageLoadResult{}, err
			}
			if terminal != nil && !sameClaudeUsageSample(*terminal, sample) {
				return application.ClaudeUsageLoadResult{}, xerrors.Errorf(
					"conflicting Claude terminal usage summaries",
				)
			}
			terminal = &sample
			result.Mode = application.ClaudeUsageModeOneShotStream
			result.BoundaryObserved = true
		}
	}
	if err := scanner.Err(); err != nil {
		return application.ClaudeUsageLoadResult{}, xerrors.Errorf("failed to scan Claude usage source")
	}
	if terminal != nil {
		result.Samples = append(result.Samples, *terminal)
	}
	result.Samples = append(result.Samples, calls...)
	return result, nil
}

func claudeAssistantUsageSample(
	envelope claudeUsageEnvelope,
	sessionID string,
) (application.ClaudeUsageSample, string, bool, error) {
	if len(envelope.Message) == 0 || string(envelope.Message) == "null" {
		return application.ClaudeUsageSample{}, "", false, nil
	}
	var message claudeAssistantMessage
	if err := json.Unmarshal(envelope.Message, &message); err != nil {
		return application.ClaudeUsageSample{}, "", false, xerrors.Errorf("invalid Claude assistant metadata")
	}
	requestID := strings.TrimSpace(envelope.RequestID)
	messageID := strings.TrimSpace(message.ID)
	if requestID == "" || messageID == "" || strings.ContainsAny(requestID+messageID, "\r\n\x00") {
		return application.ClaudeUsageSample{}, "", false, nil
	}
	identity := requestID + "\x00" + messageID
	observedAt, err := claudeObservedAt(envelope.Timestamp)
	if err != nil {
		return application.ClaudeUsageSample{}, "", false, err
	}
	counters, available, err := decodeClaudeUsage(message.Usage)
	if err != nil {
		return application.ClaudeUsageSample{}, "", false, err
	}
	return application.ClaudeUsageSample{
		RecordID:      "transcript_calls:" + opaqueClaudeIdentity(sessionID, identity),
		SourceName:    "transcript_calls",
		SourceVersion: "schema-v1",
		Model:         strings.TrimSpace(message.Model),
		Scope:         types.UsageScopeCall,
		ObservedAt:    observedAt,
		TerminalCode:  types.UsageTerminalSuccess,
		Available:     available,
		Counters:      counters,
	}, identity, true, nil
}

func claudeResultUsageSample(
	envelope claudeUsageEnvelope,
	sessionID string,
) (application.ClaudeUsageSample, error) {
	observedAt, err := claudeObservedAt(envelope.Timestamp)
	if err != nil {
		return application.ClaudeUsageSample{}, err
	}
	counters, available, err := decodeClaudeUsage(envelope.Usage)
	if err != nil {
		return application.ClaudeUsageSample{}, err
	}
	terminal := types.UsageTerminalUnknown
	if envelope.IsError || (envelope.Subtype != "" && envelope.Subtype != "success") {
		terminal = types.UsageTerminalFailure
	} else if envelope.Subtype == "success" {
		terminal = types.UsageTerminalSuccess
	}
	return application.ClaudeUsageSample{
		RecordID:      "one_shot_stream:" + opaqueClaudeIdentity(sessionID, "terminal-result"),
		SourceName:    "one_shot_stream",
		SourceVersion: "schema-v1",
		Model:         singleClaudeModel(envelope.ModelUsage),
		Scope:         types.UsageScopeRun,
		ObservedAt:    observedAt,
		TerminalCode:  terminal,
		Available:     available,
		Counters:      counters,
	}, nil
}

func decodeClaudeUsage(raw json.RawMessage) (application.ClaudeUsageCounters, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return application.ClaudeUsageCounters{}, false, nil
	}
	var counters claudeRawUsageCounters
	if err := json.Unmarshal(raw, &counters); err != nil {
		return application.ClaudeUsageCounters{}, false, xerrors.Errorf("invalid Claude usage counters")
	}
	for _, counter := range []*int64{
		counters.InputTokens, counters.CacheReadTokens, counters.CacheCreationTokens, counters.OutputTokens,
	} {
		if counter != nil && *counter < 0 {
			return application.ClaudeUsageCounters{}, false, xerrors.Errorf("invalid negative Claude usage counter")
		}
	}
	if counters.InputTokens == nil || counters.OutputTokens == nil {
		return application.ClaudeUsageCounters{}, false, xerrors.Errorf("incomplete Claude usage counters")
	}
	return application.ClaudeUsageCounters{
		InputTokens:           counters.InputTokens,
		CachedInputTokens:     counters.CacheReadTokens,
		CacheWriteInputTokens: counters.CacheCreationTokens,
		OutputTokens:          counters.OutputTokens,
	}, true, nil
}

func claudeObservedAt(raw string) (time.Time, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Unix(0, 0).UTC(), nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, xerrors.Errorf("invalid Claude usage timestamp")
	}
	return parsed.UTC(), nil
}

func opaqueClaudeIdentity(sessionID, sourceIdentity string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + sourceIdentity))
	return hex.EncodeToString(sum[:])
}

func singleClaudeModel(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var models map[string]json.RawMessage
	if json.Unmarshal(raw, &models) != nil || len(models) != 1 {
		return ""
	}
	for model := range models {
		return strings.TrimSpace(model)
	}
	return ""
}

func sameClaudeUsageSample(left, right application.ClaudeUsageSample) bool {
	if left.RecordID != right.RecordID || left.SourceName != right.SourceName ||
		left.SourceVersion != right.SourceVersion || left.Model != right.Model ||
		left.Scope != right.Scope || !left.ObservedAt.Equal(right.ObservedAt) ||
		left.TerminalCode != right.TerminalCode || left.Available != right.Available {
		return false
	}
	leftCounters := left.Counters
	rightCounters := right.Counters
	return sameOptionalInt64(leftCounters.InputTokens, rightCounters.InputTokens) &&
		sameOptionalInt64(leftCounters.CachedInputTokens, rightCounters.CachedInputTokens) &&
		sameOptionalInt64(leftCounters.CacheWriteInputTokens, rightCounters.CacheWriteInputTokens) &&
		sameOptionalInt64(leftCounters.OutputTokens, rightCounters.OutputTokens) &&
		sameOptionalInt64(leftCounters.ReasoningOutputTokens, rightCounters.ReasoningOutputTokens) &&
		sameOptionalInt64(leftCounters.TotalTokens, rightCounters.TotalTokens)
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}
