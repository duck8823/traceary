package queryservice

import (
	"errors"
	"testing"

	apptypes "github.com/duck8823/traceary/application/types"
)

func TestLiteralSearchProgressCharacterization(t *testing.T) {
	budget := apptypes.LiteralSearchBudget{SourceRows: 2, StoredBytes: 10, DecodedBytes: 10}
	t.Run("initial oversize is retryable error", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 2, budget, 2)
		_, err := p.ObserveSource(LiteralSourceObservation{Sequence: 1, Eligible: true, Stored: 11, Decoded: 1})
		var oversized *apptypes.SearchProjectionOversizeError
		if !errors.As(err, &oversized) {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("skip then oversize returns partial anchored at skip", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 3, budget, 2)
		_, _ = p.ObserveSource(LiteralSourceObservation{Sequence: 1})
		d, err := p.ObserveSource(LiteralSourceObservation{Sequence: 2, Eligible: true, Stored: 11})
		if err != nil || !d.Stop || d.PartialReason != "stored_bytes" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		got := p.FinishPage(true)
		if got.Processed != 1 || got.Examined != 2 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("hydration oversize retries before match", func(t *testing.T) {
		p := NewLiteralSearchProgress(1, 3, budget, 2)
		d, _ := p.ObserveSource(LiteralSourceObservation{Sequence: 2, Eligible: true, Stored: 6, Decoded: 6})
		if !d.Verify {
			t.Fatalf("decision=%+v", d)
		}
		d, err := p.FinishVerification(2, true)
		var oversized *apptypes.SearchProjectionOversizeError
		if !errors.As(err, &oversized) || d.Stop {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		if got := p.FinishPage(true); got.Processed != 1 {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("zero matches advance and complete without continuation", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 2, budget, 2)
		_, _ = p.ObserveSource(LiteralSourceObservation{Sequence: 1})
		_, _ = p.ObserveSource(LiteralSourceObservation{Sequence: 2})
		got := p.FinishPage(false)
		if !got.Complete || got.Processed != 2 || got.Examined != 2 || got.PartialReason != "" {
			t.Fatalf("result=%+v", got)
		}
	})
	t.Run("result and source limits are distinct", func(t *testing.T) {
		p := NewLiteralSearchProgress(0, 4, budget, 1)
		_, _ = p.ObserveSource(LiteralSourceObservation{Sequence: 1, Eligible: true, Stored: 1, Decoded: 1})
		d, err := p.FinishVerification(1, true)
		if err != nil || !d.Stop || d.PartialReason != "result_limit" {
			t.Fatalf("decision=%+v error=%v", d, err)
		}
		got := p.FinishPage(true)
		if got.Processed != 1 || got.Examined != 1 {
			t.Fatalf("result=%+v", got)
		}
	})
}

func TestLiteralSearchProgressRejectsPhaseViolations(t *testing.T) {
	p := NewLiteralSearchProgress(0, 2, apptypes.LiteralSearchBudget{SourceRows: 2, StoredBytes: 10, DecodedBytes: 10}, 2)
	_, _ = p.ObserveSource(LiteralSourceObservation{Sequence: 1, Eligible: true, Stored: 1, Decoded: 1})
	if _, err := p.ObserveSource(LiteralSourceObservation{Sequence: 2}); err == nil {
		t.Fatal("accepted source while verifying")
	}
	if _, err := p.FinishVerification(2, false); err == nil {
		t.Fatal("accepted wrong sequence")
	}
}
