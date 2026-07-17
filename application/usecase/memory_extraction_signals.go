package usecase

import (
	"context"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

type extractionSignal struct {
	text            string
	event           *model.Event
	heuristics      bool
	allowStructured bool
	client          domtypes.Client
	kind            domtypes.EventKind
	sourceHook      string
	skipReason      string
}

func (u *memoryExtractionUsecase) collectCandidateSpecs(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]memoryCandidateSpec, error) {
	signals, err := u.collectExtractionSignals(ctx, session, eventLimit)
	if err != nil {
		return nil, err
	}

	rememberContextSignals, err := u.collectRememberIntentContextSignals(ctx, session, eventLimit)
	if err != nil {
		return nil, err
	}
	rememberSpecs, err := collectRememberIntentContextSpecs(session.SessionID(), rememberContextSignals)
	if err != nil {
		return nil, err
	}
	genericSpecs := make([]memoryCandidateSpec, 0, len(signals))
	for _, signal := range signals {
		evidenceRefs, err := buildSignalEvidenceRefs(session.SessionID(), signal.event)
		if err != nil {
			return nil, err
		}
		signalSpecs, err := extractMemoryCandidatesFromSignal(signal, evidenceRefs)
		if err != nil {
			return nil, err
		}
		for _, spec := range signalSpecs {
			if spec.source == domtypes.MemorySourceRememberIntent {
				rememberSpecs = append(rememberSpecs, spec)
				continue
			}
			genericSpecs = append(genericSpecs, spec)
		}
	}

	specs := make([]memoryCandidateSpec, 0, len(rememberSpecs)+len(genericSpecs))
	specs = append(specs, rememberSpecs...)
	specs = append(specs, genericSpecs...)
	return specs, nil
}

func collectRememberIntentContextSpecs(sessionID domtypes.SessionID, signals []extractionSignal) ([]memoryCandidateSpec, error) {
	timeline := make([]extractionSignal, 0, len(signals))
	for _, signal := range signals {
		if signal.event == nil {
			continue
		}
		if signal.kind != domtypes.EventKindPrompt && signal.kind != domtypes.EventKindTranscript {
			continue
		}
		timeline = append(timeline, signal)
	}
	sort.SliceStable(timeline, func(i, j int) bool {
		left := timeline[i].event.CreatedAt()
		right := timeline[j].event.CreatedAt()
		if left.Equal(right) {
			return timeline[i].event.EventID().String() < timeline[j].event.EventID().String()
		}
		return left.Before(right)
	})

	specs := make([]memoryCandidateSpec, 0)
	for index, signal := range timeline {
		if signal.kind != domtypes.EventKindPrompt || !isShortRememberIntentSegment(signal.text) {
			continue
		}
		contextSignal, ok := previousRememberIntentContext(timeline[:index])
		if !ok {
			continue
		}
		fact := rememberIntentContextFact(contextSignal.text)
		if fact == "" {
			continue
		}
		rememberEvidenceRefs, err := buildSignalEvidenceRefs(sessionID, signal.event)
		if err != nil {
			return nil, err
		}
		contextEvidenceRefs, err := buildSignalEvidenceRefs(sessionID, contextSignal.event)
		if err != nil {
			return nil, err
		}
		evidenceRefs := mergeEvidenceRefs(rememberEvidenceRefs, contextEvidenceRefs)
		spec, ok, err := buildMemoryCandidateSpec(inferMemoryTypeForExplicitIntent(fact), fact, evidenceRefs, false)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		spec.intent.explicitRemember = true
		spec.source = domtypes.MemorySourceRememberIntent
		spec.signalScore = memoryExtractionSignalScore(spec)
		specs = append(specs, spec)
	}
	// Reverse so the newest remember-intent prompt comes first. Extract
	// applies CandidateLimit by first-seen key, so without this older context
	// would consume the budget before more recent facts.
	slices.Reverse(specs)
	return specs, nil
}

// rememberIntentContextLookback bounds how far back a short remember-only
// prompt scans for adjacent factual context. Anything further away than the
// last few prompt/transcript turns is treated as unrelated stale chatter and
// will not be used as evidence for the candidate.
const rememberIntentContextLookback = 3

func previousRememberIntentContext(signals []extractionSignal) (extractionSignal, bool) {
	examined := 0
	for index := len(signals) - 1; index >= 0; index-- {
		signal := signals[index]
		if signal.kind != domtypes.EventKindPrompt && signal.kind != domtypes.EventKindTranscript {
			continue
		}
		examined++
		if rememberIntentContextFact(signal.text) != "" {
			return signal, true
		}
		if examined >= rememberIntentContextLookback {
			return extractionSignal{}, false
		}
	}
	return extractionSignal{}, false
}

func rememberIntentContextFact(text string) string {
	segments := candidateSegments(text)
	for index := len(segments) - 1; index >= 0; index-- {
		segment := segments[index]
		if isShortRememberIntentSegment(segment) {
			continue
		}
		if fact, ok := rememberIntentFactFromSegment(segment); ok {
			return boundRememberIntentContext(fact)
		}
		fact := normalizeCandidateFact(segment)
		if fact == "" {
			continue
		}
		if _, ok := inferMemoryTypeFromText(fact); !ok {
			continue
		}
		if len(classifyExtractionNoise(fact)) > 0 {
			continue
		}
		return boundRememberIntentContext(fact)
	}
	return ""
}

func (u *memoryExtractionUsecase) collectExtractionSignals(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]extractionSignal, error) {
	signals := make([]extractionSignal, 0, 1+5*max(eventLimit, 1))
	if summary := strings.TrimSpace(session.Summary()); summary != "" {
		signals = append(signals, extractionSignal{
			text:            summary,
			heuristics:      true,
			allowStructured: true,
			kind:            domtypes.EventKind("session_summary"),
		})
	}

	appendKindSignals := func(kind domtypes.EventKind, heuristics bool, allowStructured bool) error {
		if eventLimit == 0 {
			return nil
		}
		events, err := u.eventQuery.ListRecent(
			ctx,
			eventLimit,
			0,
			kind,
			domtypes.Client(""),
			domtypes.Agent(""),
			session.SessionID(),
			domtypes.Workspace(""),
			false,
			time.Time{},
			time.Time{},
			"",
		)
		if err != nil {
			return xerrors.Errorf("failed to list %s events for extraction: %w", kind, err)
		}
		for _, event := range events {
			body := apptypes.ExtractPlainBody(event.Body())
			skipReason := ""
			if kind == domtypes.EventKindCompactSummary {
				skipReason = compactSummaryExtractionSkipReason(event, body)
			}
			signals = append(signals, extractionSignal{
				text:            body,
				event:           event,
				heuristics:      heuristics,
				allowStructured: allowStructured,
				client:          event.Client(),
				kind:            event.Kind(),
				sourceHook:      event.SourceHook(),
				skipReason:      skipReason,
			})
		}
		return nil
	}

	if err := appendKindSignals(domtypes.EventKindPrompt, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindTranscript, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindReviewed, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindNote, true, true); err != nil {
		return nil, err
	}
	if err := appendKindSignals(domtypes.EventKindCompactSummary, true, true); err != nil {
		return nil, err
	}

	return signals, nil
}

func (u *memoryExtractionUsecase) collectRememberIntentContextSignals(ctx context.Context, session apptypes.SessionSummary, eventLimit int) ([]extractionSignal, error) {
	if eventLimit == 0 {
		return nil, nil
	}
	kinds := []domtypes.EventKind{domtypes.EventKindPrompt, domtypes.EventKindTranscript}
	signals := make([]extractionSignal, 0, len(kinds)*eventLimit)
	for _, kind := range kinds {
		events, err := u.eventQuery.ListRecent(
			ctx,
			eventLimit,
			0,
			kind,
			domtypes.Client(""),
			domtypes.Agent(""),
			session.SessionID(),
			domtypes.Workspace(""),
			false,
			time.Time{},
			time.Time{},
			"",
		)
		if err != nil {
			return nil, xerrors.Errorf("failed to list %s events for remember-intent context: %w", kind, err)
		}
		for _, event := range events {
			signals = append(signals, extractionSignal{
				text:       apptypes.ExtractPlainBody(event.Body()),
				event:      event,
				client:     event.Client(),
				kind:       event.Kind(),
				sourceHook: event.SourceHook(),
			})
		}
	}
	return signals, nil
}

func compactSummaryExtractionSkipReason(event *model.Event, body string) string {
	if event == nil {
		return "missing_event"
	}
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return "empty_summary"
	}
	if event.SourceHook() == "pre_compact" || strings.HasPrefix(trimmed, domtypes.EventBodyMarkerCompactPreSnapshot) {
		return "pre_compact_snapshot"
	}
	lower := strings.ToLower(trimmed)
	switch lower {
	case "manual", "auto", "automatic", "compact triggered", "triggered", "clear", "cleared", "context cleared", "reset", "reset context", "context reset":
		return "marker_only_context_boundary"
	default:
		return ""
	}
}
