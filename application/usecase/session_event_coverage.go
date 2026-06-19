package usecase

import "github.com/duck8823/traceary/domain/types"

// EventCoverageInput is one observation fed to SummarizeSessionEventCoverage.
// It carries just enough state for the classifier to attribute the event to a
// session and decide whether it enriches that session beyond pure boundary
// metadata.
type EventCoverageInput struct {
	SessionID string
	Kind      types.EventKind
}

// SessionEventCoverage reports how many recent sessions captured prompt,
// transcript, and command_executed events versus boundaries only. It is the
// summary surfaced by the `<client>-event-coverage` doctor diagnostic that
// flags hook installs which only wire session start/end.
type SessionEventCoverage struct {
	// Sessions counts only sessions whose session_started event was
	// observed in the scanned window. List queries return newest-first, so
	// observing the start means every subsequent event of that session is
	// also in the window — this avoids misclassifying a truncated session
	// (start out of window) as boundary-only.
	Sessions       int
	BoundaryOnly   int
	Enriched       int
	WithPrompt     int
	WithTranscript int
	WithCommand    int
}

// BoundaryOnlyRatio returns BoundaryOnly / Sessions, or 0 when no sessions
// were counted (so callers can compare against a threshold without guarding
// against division by zero).
func (s SessionEventCoverage) BoundaryOnlyRatio() float64 {
	if s.Sessions == 0 {
		return 0
	}
	return float64(s.BoundaryOnly) / float64(s.Sessions)
}

// SummarizeSessionEventCoverage classifies the given event observations into
// per-session coverage counts. Enrichment kinds are
// {prompt, transcript, command_executed}; everything else (session boundaries,
// compact summaries, notes, …) is neutral. The function is pure and
// client-agnostic so the same classifier can back the gemini and claude
// coverage diagnostics.
func SummarizeSessionEventCoverage(events []EventCoverageInput) SessionEventCoverage {
	type sessionState struct {
		started    bool
		prompt     bool
		transcript bool
		command    bool
	}
	states := map[string]*sessionState{}
	for _, e := range events {
		if e.SessionID == "" {
			continue
		}
		state, ok := states[e.SessionID]
		if !ok {
			state = &sessionState{}
			states[e.SessionID] = state
		}
		switch e.Kind {
		case types.EventKindSessionStarted:
			state.started = true
		case types.EventKindPrompt:
			state.prompt = true
		case types.EventKindTranscript:
			state.transcript = true
		case types.EventKindCommandExecuted:
			state.command = true
		}
	}

	summary := SessionEventCoverage{}
	for _, state := range states {
		if !state.started {
			continue
		}
		summary.Sessions++
		if state.prompt {
			summary.WithPrompt++
		}
		if state.transcript {
			summary.WithTranscript++
		}
		if state.command {
			summary.WithCommand++
		}
		if state.prompt || state.transcript || state.command {
			summary.Enriched++
		} else {
			summary.BoundaryOnly++
		}
	}
	return summary
}
