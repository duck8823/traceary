package filesystem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"math"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

const defaultGeminiUsageMaxLineBytes = 8 * 1024 * 1024

type geminiHeadlessUsageStreamFactory struct {
	maxLineBytes int
}

// NewGeminiHeadlessUsageStreamFactory creates body-free adapters for
// Traceary-owned Gemini `stream-json` invocations.
func NewGeminiHeadlessUsageStreamFactory() application.GeminiHeadlessUsageStreamFactory {
	return &geminiHeadlessUsageStreamFactory{maxLineBytes: defaultGeminiUsageMaxLineBytes}
}

func (f *geminiHeadlessUsageStreamFactory) New(destination io.Writer) application.GeminiHeadlessUsageStream {
	if destination == nil {
		destination = io.Discard
	}
	return &geminiHeadlessUsageStream{
		destination: destination,
		maxLine:     f.maxLineBytes,
	}
}

type geminiHeadlessUsageStream struct {
	destination    io.Writer
	maxLine        int
	buffer         []byte
	sessionID      string
	initModel      string
	result         application.GeminiUsageLoadResult
	parseErr       error
	discardLine    bool
	terminalSeen   bool
	terminalDigest [sha256.Size]byte
}

func (s *geminiHeadlessUsageStream) Write(data []byte) (int, error) {
	written, err := s.destination.Write(data)
	if err != nil {
		return written, xerrors.Errorf("failed to forward Gemini headless output: %w", err)
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if s.parseErr == nil {
		s.consume(data)
	}
	return written, nil
}

func (s *geminiHeadlessUsageStream) consume(data []byte) {
	for len(data) > 0 {
		newline := bytes.IndexByte(data, '\n')
		chunk := data
		complete := false
		if newline >= 0 {
			chunk = data[:newline]
			data = data[newline+1:]
			complete = true
		} else {
			data = nil
		}
		if !s.discardLine {
			if len(s.buffer)+len(chunk) > s.maxLine {
				s.parseErr = xerrors.Errorf("Gemini headless usage line exceeds %d-byte limit", s.maxLine)
				s.buffer = nil
				s.discardLine = true
			} else {
				s.buffer = append(s.buffer, chunk...)
			}
		}
		if complete {
			if !s.discardLine && len(bytes.TrimSpace(s.buffer)) > 0 {
				if err := s.parseLine(s.buffer); err != nil {
					s.parseErr = err
				}
			}
			s.buffer = nil
			s.discardLine = false
		}
	}
}

func (s *geminiHeadlessUsageStream) Complete() (application.GeminiUsageLoadResult, error) {
	if s.parseErr == nil && !s.discardLine && len(bytes.TrimSpace(s.buffer)) > 0 {
		s.parseErr = s.parseLine(s.buffer)
	}
	s.buffer = nil
	if s.parseErr != nil {
		return application.GeminiUsageLoadResult{}, s.parseErr
	}
	return s.result, nil
}

type geminiStreamEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	SessionID string          `json:"session_id"`
	Model     string          `json:"model"`
	Status    string          `json:"status"`
	Stats     json.RawMessage `json:"stats"`
}

type geminiStreamCounters struct {
	TotalTokens  *int64 `json:"total_tokens"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	Cached       *int64 `json:"cached"`
	Input        *int64 `json:"input"`
}

type geminiStreamStats struct {
	geminiStreamCounters
	Models map[string]geminiStreamCounters `json:"models"`
}

func (s *geminiHeadlessUsageStream) parseLine(line []byte) error {
	var envelope geminiStreamEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return xerrors.Errorf("invalid Gemini headless JSON event")
	}
	switch envelope.Type {
	case "init":
		sessionID := strings.TrimSpace(envelope.SessionID)
		modelName := strings.TrimSpace(envelope.Model)
		if !validGeminiUsageIdentity(sessionID) || !validGeminiUsageIdentity(modelName) {
			return xerrors.Errorf("invalid Gemini headless init identity")
		}
		if s.sessionID != "" && (s.sessionID != sessionID || s.initModel != modelName) {
			return xerrors.Errorf("conflicting Gemini headless init identity")
		}
		s.sessionID = sessionID
		s.initModel = modelName
	case "result":
		return s.parseResult(envelope, sha256.Sum256(line))
	}
	return nil
}

func (s *geminiHeadlessUsageStream) parseResult(
	envelope geminiStreamEnvelope,
	digest [sha256.Size]byte,
) error {
	if s.sessionID == "" {
		return xerrors.Errorf("Gemini headless result arrived before init identity")
	}
	terminal, err := geminiTerminalCode(envelope.Status)
	if err != nil {
		return err
	}
	observedAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(envelope.Timestamp))
	if err != nil {
		return xerrors.Errorf("invalid Gemini headless result timestamp")
	}
	if s.terminalSeen {
		if s.terminalDigest != digest {
			return xerrors.Errorf("conflicting Gemini headless terminal results")
		}
		return nil
	}
	s.terminalSeen = true
	s.terminalDigest = digest
	if len(envelope.Stats) == 0 || string(envelope.Stats) == "null" {
		return nil
	}
	var stats geminiStreamStats
	if err := json.Unmarshal(envelope.Stats, &stats); err != nil {
		return xerrors.Errorf("invalid Gemini headless terminal usage")
	}
	if err := validateGeminiStreamCounters(stats.geminiStreamCounters); err != nil {
		return err
	}
	modelNames := make([]string, 0, len(stats.Models))
	for modelName := range stats.Models {
		modelNames = append(modelNames, modelName)
	}
	sort.Strings(modelNames)

	samples := make([]application.GeminiUsageSample, 0, max(1, len(modelNames)))
	if len(modelNames) == 0 {
		samples = append(samples, geminiUsageSample(
			s.sessionID, s.initModel, observedAt.UTC(), terminal, stats.geminiStreamCounters,
		))
	} else {
		var sums geminiCounterSums
		for _, modelName := range modelNames {
			if !validGeminiUsageIdentity(modelName) {
				return xerrors.Errorf("invalid Gemini headless model identity")
			}
			counters := stats.Models[modelName]
			if err := validateGeminiStreamCounters(counters); err != nil {
				return err
			}
			if err := sums.add(counters); err != nil {
				return err
			}
			samples = append(samples, geminiUsageSample(
				s.sessionID, modelName, observedAt.UTC(), terminal, counters,
			))
		}
		if !sums.matches(stats.geminiStreamCounters) {
			return xerrors.Errorf("Gemini headless model totals conflict with aggregate usage")
		}
	}

	next := application.GeminiUsageLoadResult{Samples: samples, BoundaryObserved: true}
	s.result = next
	return nil
}

func geminiTerminalCode(status string) (types.UsageTerminalCode, error) {
	switch strings.TrimSpace(status) {
	case "success":
		return types.UsageTerminalSuccess, nil
	case "error":
		return types.UsageTerminalFailure, nil
	default:
		return "", xerrors.Errorf("invalid Gemini headless terminal status")
	}
}

func validGeminiUsageIdentity(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && len(trimmed) <= 512 && !strings.ContainsAny(trimmed, "\r\n\x00")
}

func validateGeminiStreamCounters(counters geminiStreamCounters) error {
	values := []*int64{
		counters.TotalTokens, counters.InputTokens, counters.OutputTokens,
		counters.Cached, counters.Input,
	}
	for _, value := range values {
		if value == nil || *value < 0 {
			return xerrors.Errorf("incomplete or negative Gemini headless terminal usage")
		}
	}
	inputBreakdown, ok := checkedGeminiCounterAdd(*counters.Input, *counters.Cached)
	if !ok || inputBreakdown != *counters.InputTokens {
		return xerrors.Errorf("Gemini headless input breakdown conflicts with input total")
	}
	return nil
}

func geminiUsageSample(
	sessionID, modelName string,
	observedAt time.Time,
	terminal types.UsageTerminalCode,
	counters geminiStreamCounters,
) application.GeminiUsageSample {
	digest := sha256.Sum256([]byte(sessionID + "\x00" + modelName))
	return application.GeminiUsageSample{
		RecordID:      "headless_stream:" + hex.EncodeToString(digest[:]),
		SourceName:    "headless_stream",
		SourceVersion: "schema-v1",
		Model:         modelName,
		ObservedAt:    observedAt,
		TerminalCode:  terminal,
		Available:     true,
		Counters: application.GeminiUsageCounters{
			InputTokens:       counters.InputTokens,
			CachedInputTokens: counters.Cached,
			OutputTokens:      counters.OutputTokens,
			TotalTokens:       counters.TotalTokens,
		},
	}
}

type geminiCounterSums struct {
	total, input, output, cached, uncached int64
}

func (s *geminiCounterSums) add(counters geminiStreamCounters) error {
	values := []struct {
		sum   *int64
		value int64
	}{
		{sum: &s.total, value: *counters.TotalTokens},
		{sum: &s.input, value: *counters.InputTokens},
		{sum: &s.output, value: *counters.OutputTokens},
		{sum: &s.cached, value: *counters.Cached},
		{sum: &s.uncached, value: *counters.Input},
	}
	for _, item := range values {
		next, ok := checkedGeminiCounterAdd(*item.sum, item.value)
		if !ok {
			return xerrors.Errorf("Gemini headless model totals overflow aggregate usage")
		}
		*item.sum = next
	}
	return nil
}

func checkedGeminiCounterAdd(left, right int64) (int64, bool) {
	if left < 0 || right < 0 || right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func (s geminiCounterSums) matches(counters geminiStreamCounters) bool {
	return s.total == *counters.TotalTokens &&
		s.input == *counters.InputTokens &&
		s.output == *counters.OutputTokens &&
		s.cached == *counters.Cached &&
		s.uncached == *counters.Input
}
