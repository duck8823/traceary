package queryservice

import (
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// LiteralSourceObservation describes the persistence facts for one ordered source.
type LiteralSourceObservation struct {
	Sequence        int64
	Stored, Decoded int64
	Eligible        bool
}

// LiteralProgressDecision tells the persistence driver what operation is safe next.
type LiteralProgressDecision struct {
	Verify        bool
	Stop          bool
	PartialReason string
}

// LiteralSearchProgress is the narrow driver-facing view of application search progress.
// Its implementation and phase state remain private to this package.
type LiteralSearchProgress interface {
	ObserveSource(LiteralSourceObservation) (LiteralProgressDecision, error)
	FinishVerification(sequence int64, matched bool) (LiteralProgressDecision, error)
	FinishPage(moreSources bool) LiteralProgressResult
}

// LiteralProgressResult is the application-owned page progress decision.
type LiteralProgressResult struct {
	Processed, Examined int64
	Complete            bool
	PartialReason       string
}

type literalProgressPhase uint8

const (
	literalProgressReady literalProgressPhase = iota
	literalProgressVerifying
	literalProgressStopped
)

type literalSearchProgress struct {
	ledger      apptypes.LiteralVerificationLedger
	start       int64
	highWater   int64
	sourceLimit int
	resultLimit int
	examined    int64
	results     int
	phase       literalProgressPhase
	partial     string
	pending     LiteralSourceObservation
}

// NewLiteralSearchProgress creates the private application state machine.
func NewLiteralSearchProgress(start, highWater int64, budget apptypes.LiteralSearchBudget, resultLimit int) LiteralSearchProgress {
	return &literalSearchProgress{ledger: apptypes.LiteralVerificationLedger{Budget: budget, FullyProcessed: start}, start: start, highWater: highWater, sourceLimit: budget.SourceRows, resultLimit: resultLimit}
}

func (p *literalSearchProgress) ObserveSource(source LiteralSourceObservation) (LiteralProgressDecision, error) {
	if p.phase != literalProgressReady {
		return LiteralProgressDecision{}, fmt.Errorf("literal search progress: source observed in phase %d", p.phase)
	}
	if p.examined >= int64(p.sourceLimit) {
		return p.stop("source_rows"), nil
	}
	p.examined++
	if !source.Eligible {
		p.ledger.Skip(source.Sequence)
		return LiteralProgressDecision{}, nil
	}
	if err := p.ledger.AdmitVerification(source.Stored, source.Decoded); err != nil {
		if p.ledger.FullyProcessed == p.start {
			return LiteralProgressDecision{}, fmt.Errorf("admit literal verification: %w", err)
		}
		return p.stop(progressBudgetReason(err)), nil
	}
	p.pending = source
	p.phase = literalProgressVerifying
	return LiteralProgressDecision{Verify: true}, nil
}

func (p *literalSearchProgress) FinishVerification(sequence int64, matched bool) (LiteralProgressDecision, error) {
	if p.phase != literalProgressVerifying || sequence != p.pending.Sequence {
		return LiteralProgressDecision{}, fmt.Errorf("literal search progress: verification completed out of phase")
	}
	if err := p.ledger.FinishVerification(sequence, p.pending.Stored, p.pending.Decoded, matched); err != nil {
		if p.ledger.FullyProcessed == p.start {
			return LiteralProgressDecision{}, fmt.Errorf("finish matched literal verification: %w", err)
		}
		return p.stop(progressBudgetReason(err)), nil
	}
	p.phase = literalProgressReady
	if matched {
		p.results++
		if p.results >= p.resultLimit {
			return p.stop("result_limit"), nil
		}
	}
	return LiteralProgressDecision{}, nil
}

func (p *literalSearchProgress) FinishPage(moreSources bool) LiteralProgressResult {
	complete := p.ledger.FullyProcessed >= p.highWater
	partial := p.partial
	if !complete && partial == "" && moreSources {
		partial = "source_rows"
	}
	return LiteralProgressResult{Processed: p.ledger.FullyProcessed, Examined: p.examined, Complete: complete, PartialReason: partial}
}

func (p *literalSearchProgress) stop(reason string) LiteralProgressDecision {
	p.phase = literalProgressStopped
	p.partial = reason
	return LiteralProgressDecision{Stop: true, PartialReason: reason}
}

func progressBudgetReason(err error) string {
	var oversized *apptypes.SearchProjectionOversizeError
	if errors.As(err, &oversized) {
		return oversized.Class
	}
	return "resource_budget"
}
