package filesystem

import (
	"bytes"
	"encoding/json"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

type codexHeadlessUsageStreamFactory struct {
	now          func() time.Time
	maxLineBytes int
}

// NewCodexHeadlessUsageStreamFactory creates body-free streaming adapters for
// Traceary-owned `codex exec --json` invocations.
func NewCodexHeadlessUsageStreamFactory() application.CodexHeadlessUsageStreamFactory {
	return &codexHeadlessUsageStreamFactory{now: time.Now, maxLineBytes: defaultCodexUsageMaxLineBytes}
}

func (f *codexHeadlessUsageStreamFactory) New(destination io.Writer) application.CodexHeadlessUsageStream {
	if destination == nil {
		destination = io.Discard
	}
	return &codexHeadlessUsageStream{
		destination: destination,
		now:         f.now,
		maxLine:     f.maxLineBytes,
	}
}

type codexHeadlessUsageStream struct {
	destination io.Writer
	now         func() time.Time
	maxLine     int
	buffer      []byte
	threadID    string
	ordinal     int
	result      application.CodexUsageLoadResult
	parseErr    error
	discardLine bool
}

func (s *codexHeadlessUsageStream) Write(data []byte) (int, error) {
	written, err := s.destination.Write(data)
	if err != nil {
		return written, xerrors.Errorf("failed to forward Codex headless output: %w", err)
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if s.parseErr != nil {
		return written, nil
	}
	s.consume(data)
	return written, nil
}

func (s *codexHeadlessUsageStream) consume(data []byte) {
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
				s.parseErr = xerrors.Errorf("Codex headless usage line exceeds %d-byte limit", s.maxLine)
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

func (s *codexHeadlessUsageStream) Complete() (application.CodexUsageLoadResult, error) {
	if s.parseErr == nil && !s.discardLine && len(bytes.TrimSpace(s.buffer)) > 0 {
		s.parseErr = s.parseLine(s.buffer)
	}
	s.buffer = nil
	if s.parseErr != nil {
		return application.CodexUsageLoadResult{}, s.parseErr
	}
	return s.result, nil
}

type codexHeadlessEnvelope struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Usage    json.RawMessage `json:"usage"`
}

func (s *codexHeadlessUsageStream) parseLine(line []byte) error {
	var envelope codexHeadlessEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return xerrors.Errorf("invalid Codex headless JSON event")
	}
	switch envelope.Type {
	case "thread.started":
		threadID := strings.TrimSpace(envelope.ThreadID)
		if threadID == "" || strings.ContainsAny(threadID, "\r\n\x00") {
			return xerrors.Errorf("invalid Codex headless thread identity")
		}
		s.threadID = threadID
	case "turn.completed":
		if s.threadID == "" {
			return xerrors.Errorf("Codex headless turn completed before thread identity")
		}
		var counters codexRawUsageCounters
		if len(envelope.Usage) == 0 || string(envelope.Usage) == "null" {
			return xerrors.Errorf("Codex headless turn completed without usage")
		}
		if err := json.Unmarshal(envelope.Usage, &counters); err != nil {
			return xerrors.Errorf("invalid Codex headless terminal usage")
		}
		for _, counter := range []*int64{
			counters.InputTokens, counters.CachedInputTokens, counters.CacheWriteInputTokens,
			counters.OutputTokens, counters.ReasoningOutputTokens, counters.TotalTokens,
		} {
			if counter != nil && *counter < 0 {
				return xerrors.Errorf("invalid negative Codex headless terminal usage")
			}
		}
		if counters.InputTokens == nil || counters.OutputTokens == nil {
			return xerrors.Errorf("incomplete Codex headless terminal usage")
		}
		s.ordinal++
		s.result.Samples = append(s.result.Samples, application.CodexUsageSample{
			RecordID:      "headless_stream:" + s.threadID + ":" + strconv.Itoa(s.ordinal),
			SuppressionID: "headless_stream:" + s.threadID + ":" + strconv.Itoa(s.ordinal),
			SourceName:    "headless_stream",
			SourceVersion: "schema-v1",
			ObservedAt:    s.now().UTC(),
			TerminalCode:  types.UsageTerminalSuccess,
			Available:     true,
			Counters:      applicationCodexUsageCounters(counters),
		})
		s.result.BoundaryObserved = true
	}
	return nil
}
