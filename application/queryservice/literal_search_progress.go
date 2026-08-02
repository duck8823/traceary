package queryservice

import (
	"errors"
	"fmt"

	apptypes "github.com/duck8823/traceary/application/types"
)

// LiteralSourceDisposition makes source admission observations explicit.
type LiteralSourceDisposition uint8

const (
	// LiteralSourceSkipped identifies a tombstone or criteria/candidate rejection.
	LiteralSourceSkipped LiteralSourceDisposition = iota
	// LiteralSourceEligible identifies a source that requires canonical verification.
	LiteralSourceEligible
)

// LiteralSource describes the ordered source and its verification costs.
type LiteralSource struct {
	Sequence        int64
	Stored, Decoded int64
}

// LiteralProgressAction tells the persistence driver the only safe next operation.
type LiteralProgressAction uint8

const (
	// LiteralProgressContinue advances the driver without persistence work.
	LiteralProgressContinue LiteralProgressAction = iota
	// LiteralProgressCheckDisposition permits the driver to classify a source.
	LiteralProgressCheckDisposition
	// LiteralProgressVerify permits canonical decoding and verification.
	LiteralProgressVerify
	// LiteralProgressRecordMatch accepts a match and continues scanning.
	LiteralProgressRecordMatch
	// LiteralProgressRecordMatchAndStop accepts a match and stops at the result limit.
	LiteralProgressRecordMatchAndStop
	// LiteralProgressStop stops without accepting the current source as a match.
	LiteralProgressStop
)

// LiteralProgressDecision tells the persistence driver what operation is safe next.
type LiteralProgressDecision struct {
	Action        LiteralProgressAction
	PartialReason string
	ResumeBefore  int64
}

// LiteralSearchProgress is the narrow driver-facing view of application search progress.
// Its implementation and phase state remain private to this package.
type LiteralSearchProgress interface {
	BeginSource(LiteralSource) (LiteralProgressDecision, error)
	ObserveDisposition(LiteralSourceDisposition) (LiteralProgressDecision, error)
	FinishVerification(sequence int64, matched bool) (LiteralProgressDecision, error)
	FinishPage() LiteralProgressResult
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
	literalProgressCheckingDisposition
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
	pending     LiteralSource
}

// NewLiteralSearchProgress creates the private application state machine.
func NewLiteralSearchProgress(start, highWater int64, budget apptypes.LiteralSearchBudget, resultLimit int) LiteralSearchProgress {
	return &literalSearchProgress{ledger: apptypes.LiteralVerificationLedger{Budget: budget, FullyProcessed: start}, start: start, highWater: highWater, sourceLimit: budget.SourceRows, resultLimit: resultLimit}
}

func (p *literalSearchProgress) BeginSource(source LiteralSource) (LiteralProgressDecision, error) {
	if p.phase != literalProgressReady {
		return LiteralProgressDecision{}, fmt.Errorf("literal search progress: source observed in phase %d", p.phase)
	}
	if p.examined >= int64(p.sourceLimit) {
		return p.stop("source_rows"), nil
	}
	p.examined++
	p.pending = source
	p.phase = literalProgressCheckingDisposition
	return LiteralProgressDecision{Action: LiteralProgressCheckDisposition}, nil
}

func (p *literalSearchProgress) ObserveDisposition(disposition LiteralSourceDisposition) (LiteralProgressDecision, error) {
	if p.phase != literalProgressCheckingDisposition {
		return LiteralProgressDecision{}, fmt.Errorf("literal search progress: disposition observed in phase %d", p.phase)
	}
	if disposition == LiteralSourceSkipped {
		p.ledger.Skip(p.pending.Sequence)
		p.phase = literalProgressReady
		return LiteralProgressDecision{Action: LiteralProgressContinue}, nil
	}
	if disposition != LiteralSourceEligible {
		return LiteralProgressDecision{}, fmt.Errorf("literal search progress: unknown source disposition %d", disposition)
	}
	source := p.pending
	if err := p.ledger.AdmitVerification(source.Stored, source.Decoded); err != nil {
		if p.ledger.FullyProcessed == p.start {
			return LiteralProgressDecision{}, fmt.Errorf("admit literal verification: %w", err)
		}
		return p.stop(progressBudgetReason(err)), nil
	}
	p.phase = literalProgressVerifying
	return LiteralProgressDecision{Action: LiteralProgressVerify, ResumeBefore: p.ledger.FullyProcessed}, nil
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
			decision := p.stop("result_limit")
			decision.Action = LiteralProgressRecordMatchAndStop
			return decision, nil
		}
		return LiteralProgressDecision{Action: LiteralProgressRecordMatch}, nil
	}
	return LiteralProgressDecision{Action: LiteralProgressContinue}, nil
}

func (p *literalSearchProgress) FinishPage() LiteralProgressResult {
	complete := p.ledger.FullyProcessed >= p.highWater
	partial := p.partial
	if complete {
		partial = ""
	}
	if !complete && partial == "" {
		partial = "source_rows"
	}
	return LiteralProgressResult{Processed: p.ledger.FullyProcessed, Examined: p.examined, Complete: complete, PartialReason: partial}
}

func (p *literalSearchProgress) stop(reason string) LiteralProgressDecision {
	p.phase = literalProgressStopped
	p.partial = reason
	return LiteralProgressDecision{Action: LiteralProgressStop, PartialReason: reason}
}

func progressBudgetReason(err error) string {
	var oversized *apptypes.SearchProjectionOversizeError
	if errors.As(err, &oversized) {
		return oversized.Class
	}
	return "resource_budget"
}
