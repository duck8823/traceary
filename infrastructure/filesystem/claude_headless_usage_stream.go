package filesystem

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

type claudeHeadlessUsageStreamFactory struct {
	now          func() time.Time
	maxLineBytes int
}

// NewClaudeHeadlessUsageStreamFactory creates body-free streaming adapters for
// Traceary-owned Claude print/one-shot invocations.
func NewClaudeHeadlessUsageStreamFactory() application.ClaudeHeadlessUsageStreamFactory {
	return &claudeHeadlessUsageStreamFactory{now: time.Now, maxLineBytes: defaultClaudeUsageMaxLineBytes}
}

func (f *claudeHeadlessUsageStreamFactory) New(destination io.Writer) application.ClaudeHeadlessUsageStream {
	if destination == nil {
		destination = io.Discard
	}
	return &claudeHeadlessUsageStream{
		destination: destination,
		now:         f.now,
		maxLine:     f.maxLineBytes,
		result: application.ClaudeUsageLoadResult{
			Mode: application.ClaudeUsageModeOneShotStream,
		},
	}
}

type claudeHeadlessUsageStream struct {
	destination io.Writer
	now         func() time.Time
	maxLine     int
	buffer      []byte
	result      application.ClaudeUsageLoadResult
	parseErr    error
	discardLine bool
}

func (s *claudeHeadlessUsageStream) Write(data []byte) (int, error) {
	written, err := s.destination.Write(data)
	if err != nil {
		return written, xerrors.Errorf("failed to forward Claude headless output: %w", err)
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

func (s *claudeHeadlessUsageStream) consume(data []byte) {
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
				s.parseErr = xerrors.Errorf("Claude headless usage line exceeds %d-byte limit", s.maxLine)
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

func (s *claudeHeadlessUsageStream) Complete() (application.ClaudeUsageLoadResult, error) {
	if s.parseErr == nil && !s.discardLine && len(bytes.TrimSpace(s.buffer)) > 0 {
		s.parseErr = s.parseLine(s.buffer)
	}
	s.buffer = nil
	if s.parseErr != nil {
		return s.result, s.parseErr
	}
	return s.result, nil
}

func (s *claudeHeadlessUsageStream) parseLine(line []byte) error {
	var envelope claudeUsageEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return xerrors.Errorf("invalid Claude headless JSON event")
	}
	if envelope.Type != "result" {
		return nil
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(envelope.SessionID2)
	}
	if sessionID == "" || strings.ContainsAny(sessionID, "\r\n\x00") {
		return xerrors.Errorf("invalid Claude headless session identity")
	}
	sample, err := claudeResultUsageSample(envelope, sessionID)
	if err != nil {
		return err
	}
	sample.ObservedAt = s.now().UTC()
	if len(s.result.Samples) > 0 {
		sample.ObservedAt = s.result.Samples[0].ObservedAt
		if !sameClaudeUsageSample(s.result.Samples[0], sample) {
			return xerrors.Errorf("conflicting Claude headless terminal usage summaries")
		}
		return nil
	}
	s.result.Samples = append(s.result.Samples, sample)
	s.result.BoundaryObserved = true
	return nil
}
