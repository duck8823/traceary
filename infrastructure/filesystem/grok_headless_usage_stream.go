package filesystem

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/types"
)

const defaultGrokUsageMaxLineBytes = 8 * 1024 * 1024

type grokHeadlessUsageStreamFactory struct {
	now          func() time.Time
	maxLineBytes int
}

// NewGrokHeadlessUsageStreamFactory creates body-free adapters for
// Traceary-owned Grok `streaming-json` invocations.
func NewGrokHeadlessUsageStreamFactory() application.GrokHeadlessUsageStreamFactory {
	return &grokHeadlessUsageStreamFactory{now: time.Now, maxLineBytes: defaultGrokUsageMaxLineBytes}
}

func (f *grokHeadlessUsageStreamFactory) New(destination io.Writer) application.GrokHeadlessUsageStream {
	if destination == nil {
		destination = io.Discard
	}
	return &grokHeadlessUsageStream{
		destination: destination,
		now:         f.now,
		maxLine:     f.maxLineBytes,
	}
}

type grokHeadlessUsageStream struct {
	destination       io.Writer
	now               func() time.Time
	maxLine           int
	buffer            []byte
	result            application.GrokUsageLoadResult
	parseErr          error
	discardLine       bool
	terminalSeen      bool
	terminalSignature [sha256.Size]byte
	terminalRequestID string
}

func (s *grokHeadlessUsageStream) Write(data []byte) (int, error) {
	written, err := s.destination.Write(data)
	if err != nil {
		return written, xerrors.Errorf("failed to forward Grok headless output: %w", err)
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	if s.parseErr == nil {
		s.consume(data)
	}
	return written, nil
}

func (s *grokHeadlessUsageStream) consume(data []byte) {
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
				s.parseErr = xerrors.Errorf("Grok headless usage line exceeds %d-byte limit", s.maxLine)
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

func (s *grokHeadlessUsageStream) Complete() (application.GrokUsageLoadResult, error) {
	if s.parseErr == nil && !s.discardLine && len(bytes.TrimSpace(s.buffer)) > 0 {
		s.parseErr = s.parseLine(s.buffer)
	}
	s.buffer = nil
	if s.parseErr != nil {
		return application.GrokUsageLoadResult{}, s.parseErr
	}
	return s.result, nil
}

type grokStreamEnvelope struct {
	Type string `json:"type"`
}

type grokStreamEnd struct {
	Type       string                     `json:"type"`
	RequestID  string                     `json:"requestId"`
	SessionID  string                     `json:"sessionId"`
	StopReason string                     `json:"stopReason"`
	NumTurns   *int64                     `json:"num_turns"`
	Usage      *grokStreamUsage           `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

type grokStreamUsage struct {
	InputTokens       *int64 `json:"input_tokens"`
	CachedInputTokens *int64 `json:"cache_read_input_tokens"`
	OutputTokens      *int64 `json:"output_tokens"`
	ReasoningTokens   *int64 `json:"reasoning_tokens"`
	TotalTokens       *int64 `json:"total_tokens"`
}

func (s *grokHeadlessUsageStream) parseLine(line []byte) error {
	var envelope grokStreamEnvelope
	if err := json.Unmarshal(line, &envelope); err != nil {
		return xerrors.Errorf("invalid Grok headless JSON event")
	}
	if envelope.Type != "end" {
		return nil
	}
	var end grokStreamEnd
	if err := json.Unmarshal(line, &end); err != nil {
		return xerrors.Errorf("invalid Grok headless terminal event")
	}
	return s.parseEnd(end)
}

func (s *grokHeadlessUsageStream) parseEnd(end grokStreamEnd) error {
	requestID := strings.TrimSpace(end.RequestID)
	sessionID := strings.TrimSpace(end.SessionID)
	if !validGrokStreamIdentity(requestID) || !validGrokStreamIdentity(sessionID) {
		return xerrors.Errorf("invalid Grok headless terminal identity")
	}
	if end.NumTurns == nil || *end.NumTurns < 0 {
		return xerrors.Errorf("invalid Grok headless terminal turn count")
	}
	if len(end.StopReason) > 128 || strings.ContainsAny(end.StopReason, "\r\n\x00") {
		return xerrors.Errorf("invalid Grok headless terminal reason")
	}
	terminal := types.UsageTerminalUnknown
	if strings.TrimSpace(end.StopReason) == "EndTurn" {
		terminal = types.UsageTerminalSuccess
	}
	modelNames := make([]string, 0, len(end.ModelUsage))
	for modelName := range end.ModelUsage {
		modelName = strings.TrimSpace(modelName)
		if !validGrokStreamIdentity(modelName) {
			return xerrors.Errorf("invalid Grok headless model identity")
		}
		modelNames = append(modelNames, modelName)
	}
	sort.Strings(modelNames)
	model := ""
	if len(modelNames) == 1 {
		model = modelNames[0]
	}

	next := application.GrokUsageLoadResult{
		BoundaryObserved: true,
		TerminalRecordID: grokHeadlessRecordID(requestID, sessionID),
		TerminalCode:     terminal,
	}
	if end.Usage != nil {
		if err := validateGrokStreamUsage(*end.Usage); err != nil {
			return err
		}
		next.Samples = []application.GrokUsageSample{{
			RecordID:      next.TerminalRecordID,
			SourceName:    "headless_stream",
			SourceVersion: "0.2.106",
			Model:         model,
			ObservedAt:    s.now().UTC(),
			TerminalCode:  terminal,
			Available:     true,
			Counters: application.GrokUsageCounters{
				InputTokens:       end.Usage.InputTokens,
				CachedInputTokens: end.Usage.CachedInputTokens,
				OutputTokens:      end.Usage.OutputTokens,
				ReasoningTokens:   end.Usage.ReasoningTokens,
				TotalTokens:       end.Usage.TotalTokens,
			},
		}}
	}
	signature, err := grokTerminalMetadataSignature(end, modelNames)
	if err != nil {
		return err
	}
	if s.terminalSeen {
		if s.terminalRequestID != requestID || s.terminalSignature != signature {
			return xerrors.Errorf("conflicting Grok headless terminal results")
		}
		return nil
	}
	s.terminalSeen = true
	s.terminalRequestID = requestID
	s.terminalSignature = signature
	s.result = next
	return nil
}

func grokHeadlessRecordID(requestID, sessionID string) string {
	digest := sha256.Sum256([]byte(requestID + "\x00" + sessionID))
	return "headless_stream:" + hex.EncodeToString(digest[:])
}

func validateGrokStreamUsage(usage grokStreamUsage) error {
	for _, value := range []*int64{
		usage.InputTokens,
		usage.CachedInputTokens,
		usage.OutputTokens,
		usage.ReasoningTokens,
		usage.TotalTokens,
	} {
		if value == nil {
			return xerrors.Errorf("incomplete Grok headless terminal usage")
		}
		if *value < 0 {
			return xerrors.Errorf("invalid negative Grok headless terminal usage")
		}
	}
	return nil
}

func grokTerminalMetadataSignature(
	end grokStreamEnd,
	modelNames []string,
) ([sha256.Size]byte, error) {
	type terminalSignature struct {
		RequestID  string           `json:"request_id"`
		SessionID  string           `json:"session_id"`
		StopReason string           `json:"stop_reason"`
		NumTurns   int64            `json:"num_turns"`
		Usage      *grokStreamUsage `json:"usage"`
		Models     []string         `json:"models"`
	}
	normalized := terminalSignature{
		RequestID:  strings.TrimSpace(end.RequestID),
		SessionID:  strings.TrimSpace(end.SessionID),
		StopReason: strings.TrimSpace(end.StopReason),
		NumTurns:   *end.NumTurns,
		Usage:      end.Usage,
		Models:     modelNames,
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return [sha256.Size]byte{}, xerrors.Errorf("failed to normalize Grok terminal metadata: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func validGrokStreamIdentity(value string) bool {
	return value != "" && len(value) <= 512 && !strings.ContainsAny(value, "\r\n\x00")
}
