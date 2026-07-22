package usecase_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/types"
)

type oneShotRepairStoreStub struct {
	params       apptypes.OneShotRepairParams
	result       apptypes.OneShotRepairResult
	err          error
	previewCalls int
	applyCalls   int
}

func (s *oneShotRepairStoreStub) PreviewOneShotSessions(_ context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error) {
	s.previewCalls++
	s.params = params
	return s.result, s.err
}

func (s *oneShotRepairStoreStub) ApplyOneShotSessions(_ context.Context, params apptypes.OneShotRepairParams) (apptypes.OneShotRepairResult, error) {
	s.applyCalls++
	s.params = params
	return s.result, s.err
}

func TestOneShotRepairUsecase_ValidatesAuthoritativeEvidence(t *testing.T) {
	t.Parallel()
	now := time.Now()
	valid := apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("a", 64),
		StaleAfter:   24 * time.Hour,
		Now:          now,
		Entries: []apptypes.OneShotRepairEvidenceEntry{{
			SessionID: "session", RuntimeMode: types.RuntimeModeOneShot, TerminalReason: types.TerminalReasonSuccess,
			CompletedAt: now.Add(-time.Hour), EvidenceSource: apptypes.OneShotRepairEvidenceCodexExec, EvidenceRef: "sha256:evidence",
		}},
	}
	tests := []struct {
		name   string
		mutate func(*apptypes.OneShotRepairParams)
	}{
		{name: "empty entries", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries = nil }},
		{name: "duplicate session", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries = append(p.Entries, p.Entries[0]) }},
		{name: "control character in session", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].SessionID = "session\x1b" }},
		{name: "interactive assertion", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].RuntimeMode = types.RuntimeModeInteractive }},
		{name: "legacy reason", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].TerminalReason = types.TerminalReasonLegacyUnknown }},
		{name: "future completion", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].CompletedAt = now.Add(time.Hour) }},
		{name: "unknown evidence", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].EvidenceSource = "transcript_guess" }},
		{name: "unsafe evidence ref", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].EvidenceRef = "line1\nline2" }},
		{name: "terminal escape in evidence ref", mutate: func(p *apptypes.OneShotRepairParams) { p.Entries[0].EvidenceRef = "run:\x1b[31m42" }},
		{name: "invalid hash", mutate: func(p *apptypes.OneShotRepairParams) { p.EvidenceHash = "short" }},
		{name: "invalid stale duration", mutate: func(p *apptypes.OneShotRepairParams) { p.StaleAfter = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			params := valid
			params.Entries = append([]apptypes.OneShotRepairEvidenceEntry(nil), valid.Entries...)
			tc.mutate(&params)
			store := &oneShotRepairStoreStub{}
			_, err := usecase.NewOneShotRepairUsecase(store).Preview(context.Background(), params)
			if err == nil {
				t.Fatal("Repair() error = nil, want validation error")
			}
			if store.previewCalls != 0 || store.applyCalls != 0 {
				t.Fatalf("store calls = preview:%d apply:%d, want 0", store.previewCalls, store.applyCalls)
			}
		})
	}
}

func TestOneShotRepairUsecase_DelegatesValidatedParams(t *testing.T) {
	t.Parallel()
	now := time.Now()
	store := &oneShotRepairStoreStub{result: apptypes.OneShotRepairResult{EvidenceHash: strings.Repeat("b", 64)}}
	sut := usecase.NewOneShotRepairUsecase(store)
	params := apptypes.OneShotRepairParams{
		EvidenceHash: strings.Repeat("b", 64), StaleAfter: 24 * time.Hour, Now: now,
		Entries: []apptypes.OneShotRepairEvidenceEntry{{
			SessionID: "session", RuntimeMode: types.RuntimeModeOneShot, TerminalReason: types.TerminalReasonFailure,
			CompletedAt: now.Add(-time.Hour), EvidenceSource: apptypes.OneShotRepairEvidenceBatchRunner, EvidenceRef: "run:42",
		}},
	}
	result, err := sut.Preview(context.Background(), params)
	if err != nil {
		t.Fatalf("Preview() error = %v", err)
	}
	if store.previewCalls != 1 || store.applyCalls != 0 || store.params.Entries[0].TerminalReason != types.TerminalReasonFailure || result.EvidenceHash != params.EvidenceHash {
		t.Fatalf("preview delegation mismatch: preview=%d apply=%d params=%+v result=%+v", store.previewCalls, store.applyCalls, store.params, result)
	}
	if _, err := sut.Apply(context.Background(), params); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if store.previewCalls != 1 || store.applyCalls != 1 {
		t.Fatalf("apply delegation mismatch: preview=%d apply=%d", store.previewCalls, store.applyCalls)
	}
	store.err = errors.New("store failed")
	if _, err := sut.Apply(context.Background(), params); err == nil {
		t.Fatal("Apply() store error = nil")
	}
}
