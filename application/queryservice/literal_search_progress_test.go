package queryservice

import (
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestLiteralSearchProgressCharacterization(t *testing.T) {
	budget := apptypes.LiteralSearchBudget{SourceRows: 2, StoredBytes: 10, DecodedBytes: 10}
	t.Run("initial oversize is retryable partial", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 2, budget, 2)
		_, _ = p.BeginSource(LiteralSource{Sequence: 1, Stored: 11, Decoded: 1})
		d, err := p.ObserveDisposition(LiteralSourceEligible)
		if err != nil || d.Action != LiteralProgressStop || d.PartialReason != "stored_bytes" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
	})
	t.Run("skip then oversize returns partial anchored at skip", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 3, budget, 2)
		_, _ = p.BeginSource(LiteralSource{Sequence: 1})
		_, _ = p.ObserveDisposition(LiteralSourceSkipped)
		_, _ = p.BeginSource(LiteralSource{Sequence: 2, Stored: 11})
		d, err := p.ObserveDisposition(LiteralSourceEligible)
		if err != nil || d.Action != LiteralProgressStop || d.PartialReason != "stored_bytes" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		got := p.FinishPage()
		if got.Processed != 1 || got.Examined != 2 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("hydration oversize retries before match", func(t *testing.T) {
		p := NewLiteralSearchProgress(1, 3, budget, 2)
		_, _ = p.BeginSource(LiteralSource{Sequence: 2, Stored: 6, Decoded: 6})
		d, _ := p.ObserveDisposition(LiteralSourceEligible)
		if d.Action != LiteralProgressVerify {
			t.Fatalf("decision=%+v", d)
		}
		d, err := p.FinishVerification(2, true)
		if err != nil || d.Action != LiteralProgressStop || d.PartialReason != "verified_hydration_bytes" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		if got := p.FinishPage(); got.Processed != 1 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("zero matches advance and complete without continuation", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 2, budget, 2)
		_, _ = p.BeginSource(LiteralSource{Sequence: 1})
		_, _ = p.ObserveDisposition(LiteralSourceSkipped)
		_, _ = p.BeginSource(LiteralSource{Sequence: 2})
		_, _ = p.ObserveDisposition(LiteralSourceSkipped)
		got := p.FinishPage()
		if !got.Complete || got.Processed != 2 || got.Examined != 2 || got.PartialReason != "" {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("result and source limits are distinct", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 4, budget, 1)
		_, _ = p.BeginSource(LiteralSource{Sequence: 1, Stored: 1, Decoded: 1})
		_, _ = p.ObserveDisposition(LiteralSourceEligible)
		d, err := p.FinishVerification(1, true)
		if err != nil || d.Action != LiteralProgressRecordMatchAndStop || d.PartialReason != "result_limit" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		got := p.FinishPage()
		if got.Processed != 1 || got.Examined != 1 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("source row limit preserves processed and examined", func(t *testing.T) {
		p := NewLiteralSearchProgress(3, 9, apptypes.LiteralSearchBudget{SourceRows: 1, StoredBytes: 10, DecodedBytes: 10}, 2)
		_, _ = p.BeginSource(LiteralSource{Sequence: 4})
		_, _ = p.ObserveDisposition(LiteralSourceSkipped)
		d, err := p.BeginSource(LiteralSource{Sequence: 5})
		if err != nil || d.Action != LiteralProgressStop || d.PartialReason != "source_rows" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		got := p.FinishPage()
		if got.Processed != 4 || got.Examined != 1 || got.Complete {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("result limit on high water completes without partial", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 1, budget, 1)
		_, _ = p.BeginSource(LiteralSource{Sequence: 1, Stored: 1, Decoded: 1})
		_, _ = p.ObserveDisposition(LiteralSourceEligible)
		_, _ = p.FinishVerification(1, true)
		got := p.FinishPage()
		if !got.Complete || got.PartialReason != "" || got.Processed != 1 || got.Examined != 1 {
			t.Fatalf("result=%+v", got)
		}
	})
}

func TestLiteralSearchProgressRejectsPhaseViolations(t *testing.T) {
	p := NewLiteralSearchProgress(0, 2, apptypes.LiteralSearchBudget{SourceRows: 2, StoredBytes: 10, DecodedBytes: 10}, 2)
	_, _ = p.BeginSource(LiteralSource{Sequence: 1, Stored: 1, Decoded: 1})
	_, _ = p.ObserveDisposition(LiteralSourceEligible)
	if _, err := p.BeginSource(LiteralSource{Sequence: 2}); err == nil {
		t.Fatal("accepted source while verifying")
	}
	if _, err := p.FinishVerification(2, false); err == nil {
		t.Fatal("accepted wrong sequence")
	}
}
