package mcpserver

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"golang.org/x/xerrors"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
)

const eventContinuationVersion = 2

const eventContinuationAdditionalData = "traceary:mcp:event-continuation:v2"

var loadEventContinuationAEAD = sync.OnceValues(func() (cipher.AEAD, error) {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, xerrors.Errorf("failed to generate event continuation key: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, xerrors.Errorf("failed to initialize event continuation cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, xerrors.Errorf("failed to initialize event continuation authentication: %w", err)
	}
	return aead, nil
})

type eventContinuationCursor struct {
	Version         int    `json:"v"`
	Tool            string `json:"t"`
	Fingerprint     string `json:"f"`
	Snapshot        string `json:"s"`
	AnchorCreatedAt string `json:"a"`
	AnchorEventID   string `json:"i"`
}

type eventPageRequest struct {
	budget      apptypes.EventResponseBudget
	offset      int
	snapshot    string
	pageAnchor  apptypes.EventPageAnchor
	tool        string
	fingerprint string
}

type resolvedEventContinuation struct {
	fingerprint string
	snapshot    time.Time
	pageAnchor  apptypes.EventPageAnchor
}

func (c resolvedEventContinuation) isZero() bool {
	return c.snapshot.IsZero()
}

func resolveEventPageRequest(
	tool string,
	rawLimit, rawOffset int,
	continuation resolvedEventContinuation,
	fingerprint string,
	bodyRuneLimit int,
) (eventPageRequest, error) {
	if rawOffset < 0 {
		return eventPageRequest{}, xerrors.Errorf("offset must be greater than or equal to 0")
	}
	limit := normalizedEventPageLimit(rawLimit)
	budget, err := apptypes.NewEventResponseBudget(limit, bodyRuneLimit)
	if err != nil {
		return eventPageRequest{}, xerrors.Errorf("failed to resolve event response budget: %w", err)
	}
	request := eventPageRequest{budget: budget, offset: rawOffset, tool: tool, fingerprint: fingerprint}
	if continuation.isZero() {
		return request, nil
	}
	if rawOffset != 0 {
		return eventPageRequest{}, xerrors.Errorf("continuation cannot be combined with offset")
	}
	if continuation.fingerprint != fingerprint {
		return eventPageRequest{}, xerrors.Errorf("continuation does not match the requested filters or response shape")
	}
	request.offset = 0
	request.snapshot = formatEventCursorTimestamp(continuation.snapshot)
	request.pageAnchor = continuation.pageAnchor
	return request, nil
}

func normalizedEventPageLimit(rawLimit int) int {
	if rawLimit == 0 {
		return apptypes.DefaultEventResponseItemLimit
	}
	return rawLimit
}

func eventRequestFingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(part))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func encodeEventContinuation(request eventPageRequest, anchor apptypes.EventPageAnchor, snapshot string) (string, error) {
	if anchor.IsZero() || strings.TrimSpace(snapshot) == "" {
		return "", xerrors.Errorf("event continuation requires a snapshot and page anchor")
	}
	plaintext, err := json.Marshal(eventContinuationCursor{
		Version: eventContinuationVersion, Tool: request.tool, Fingerprint: request.fingerprint,
		Snapshot: snapshot, AnchorCreatedAt: formatEventCursorTimestamp(anchor.CreatedAt()),
		AnchorEventID: anchor.EventID().String(),
	})
	if err != nil {
		return "", xerrors.Errorf("failed to encode event continuation payload: %w", err)
	}
	aead, err := loadEventContinuationAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", xerrors.Errorf("failed to generate event continuation nonce: %w", err)
	}
	encoded := aead.Seal(nonce, nonce, plaintext, []byte(eventContinuationAdditionalData))
	return base64.RawURLEncoding.EncodeToString(encoded), nil
}

func decodeEventContinuation(raw string) (eventContinuationCursor, error) {
	encoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return eventContinuationCursor{}, xerrors.Errorf("continuation is malformed")
	}
	aead, err := loadEventContinuationAEAD()
	if err != nil {
		return eventContinuationCursor{}, err
	}
	if len(encoded) < aead.NonceSize()+aead.Overhead() {
		return eventContinuationCursor{}, xerrors.Errorf("continuation is malformed")
	}
	nonce := encoded[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, encoded[aead.NonceSize():], []byte(eventContinuationAdditionalData))
	if err != nil {
		return eventContinuationCursor{}, xerrors.Errorf(
			"continuation cannot be authenticated; it may have been modified or issued before the MCP server restarted",
		)
	}
	var cursor eventContinuationCursor
	if err := json.Unmarshal(plaintext, &cursor); err != nil {
		return eventContinuationCursor{}, xerrors.Errorf("continuation is malformed")
	}
	if cursor.Version != eventContinuationVersion ||
		strings.TrimSpace(cursor.Tool) == "" ||
		strings.TrimSpace(cursor.Fingerprint) == "" ||
		strings.TrimSpace(cursor.Snapshot) == "" ||
		strings.TrimSpace(cursor.AnchorCreatedAt) == "" ||
		strings.TrimSpace(cursor.AnchorEventID) == "" {
		return eventContinuationCursor{}, xerrors.Errorf("continuation has an unsupported version or invalid fields")
	}
	return cursor, nil
}

func resolveEventContinuation(tool, raw string) (resolvedEventContinuation, error) {
	if strings.TrimSpace(raw) == "" {
		return resolvedEventContinuation{}, nil
	}
	cursor, err := decodeEventContinuation(raw)
	if err != nil {
		return resolvedEventContinuation{}, err
	}
	if cursor.Tool != tool {
		return resolvedEventContinuation{}, xerrors.Errorf("continuation is for %s, not %s", cursor.Tool, tool)
	}
	snapshot, err := time.Parse(time.RFC3339Nano, cursor.Snapshot)
	if err != nil || snapshot.IsZero() {
		return resolvedEventContinuation{}, xerrors.Errorf("continuation has an invalid snapshot upper bound")
	}
	anchorCreatedAt, err := time.Parse(time.RFC3339Nano, cursor.AnchorCreatedAt)
	if err != nil {
		return resolvedEventContinuation{}, xerrors.Errorf("continuation has an invalid event page anchor")
	}
	anchorEventID, err := domtypes.EventIDFrom(cursor.AnchorEventID)
	if err != nil {
		return resolvedEventContinuation{}, xerrors.Errorf("continuation has an invalid event page anchor")
	}
	anchor, err := apptypes.EventPageAnchorOf(anchorCreatedAt, anchorEventID)
	if err != nil {
		return resolvedEventContinuation{}, xerrors.Errorf("continuation has an invalid event page anchor")
	}
	return resolvedEventContinuation{
		fingerprint: cursor.Fingerprint,
		snapshot:    snapshot.UTC(),
		pageAnchor:  anchor,
	}, nil
}

func formatEventCursorTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func formatEventFingerprintTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatEventCursorTimestamp(value)
}

func eventPageAnchorFromOutput(event eventOutput) (apptypes.EventPageAnchor, error) {
	createdAt, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
	if err != nil {
		return apptypes.EventPageAnchor{}, xerrors.Errorf("failed to parse returned event created-at: %w", err)
	}
	eventID, err := domtypes.EventIDFrom(event.EventID)
	if err != nil {
		return apptypes.EventPageAnchor{}, xerrors.Errorf("failed to parse returned event ID: %w", err)
	}
	anchor, err := apptypes.EventPageAnchorOf(createdAt, eventID)
	if err != nil {
		return apptypes.EventPageAnchor{}, xerrors.Errorf("failed to build returned event page anchor: %w", err)
	}
	return anchor, nil
}

type encodedEventBody struct {
	Body       *string                   `json:"body,omitempty"`
	BodyBlocks []apptypes.EventBodyBlock `json:"body_blocks,omitempty"`
}

func encodedEventBodyBytes(event eventOutput) int {
	encoded, err := json.Marshal(encodedEventBody{Body: event.Body, BodyBlocks: event.BodyBlocks})
	if err != nil {
		return 0
	}
	if string(encoded) == "{}" {
		return 0
	}
	return len(encoded)
}

type eventAggregateBudgetResult struct {
	events        []eventOutput
	partial       bool
	hasUnreturned bool
}

// applyEventAggregateBudget returns the longest contiguous event prefix whose
// body payload fits. It never advances past an event after stripping its body:
// that event remains eligible on the next keyset page.
func applyEventAggregateBudget(
	events []eventOutput,
	budget apptypes.EventResponseBudget,
) eventAggregateBudgetResult {
	outputs := make([]eventOutput, 0, len(events))
	used := 0
	partial := false
	for _, event := range events {
		bodyBytes := encodedEventBodyBytes(event)
		if used+bodyBytes <= budget.AggregateBodyBytes() {
			outputs = append(outputs, event)
			used += bodyBytes
			continue
		}
		partial = true
		truncated, retained := truncateEventForAggregateBudget(
			event,
			budget.AggregateBodyBytes()-used,
		)
		if !retained {
			break
		}
		outputs = append(outputs, truncated)
		used += encodedEventBodyBytes(truncated)
	}
	return eventAggregateBudgetResult{
		events:        outputs,
		partial:       partial,
		hasUnreturned: len(outputs) < len(events),
	}
}

func truncateEventForAggregateBudget(event eventOutput, available int) (eventOutput, bool) {
	original := event.Body
	originalRunes := 0
	if original != nil {
		originalRunes = len([]rune(*original))
	}
	event.BodyBlocks = nil
	if original == nil || available <= 0 {
		return eventOutput{}, false
	}
	runes := []rune(*original)
	low, high, best := 0, len(runes), -1
	for low <= high {
		middle := (low + high) / 2
		candidate := string(runes[:middle])
		if middle < len(runes) {
			candidate += apptypes.TruncationEllipsis
		}
		event.Body = &candidate
		if encodedEventBodyBytes(event) <= available {
			best = middle
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if best < 0 {
		return eventOutput{}, false
	}
	value := string(runes[:best])
	if best < len(runes) {
		value += apptypes.TruncationEllipsis
		event.BodyTruncated = true
		if event.BodyLength == 0 {
			event.BodyLength = originalRunes
		}
	}
	event.Body = &value
	// A final escaped-byte check preserves the hard aggregate invariant.
	if encodedEventBodyBytes(event) > available {
		return eventOutput{}, false
	}
	return event, true
}

type eventPageLoaders struct {
	metadataAvailable bool
	candidates        func(context.Context, int, int) ([]apptypes.EventMetadata, error)
	bounded           func(context.Context, []apptypes.EventMetadata, int) ([]apptypes.BoundedEvent, error)
	full              func(context.Context, int, int) ([]*model.Event, error)
	convertFull       func([]*model.Event) []eventOutput
	legacy            func(context.Context, int, int) ([]eventOutput, error)
}

func loadEventPage(ctx context.Context, request eventPageRequest, projection apptypes.EventProjection, loaders eventPageLoaders, interval *intervalOutput, snapshot string) (eventsOutput, error) {
	if !loaders.metadataAvailable || loaders.candidates == nil {
		if loaders.legacy == nil {
			return eventsOutput{}, xerrors.Errorf("event metadata usecase is not configured")
		}
		events, err := loaders.legacy(ctx, request.budget.ItemLimit(), request.offset)
		if err != nil {
			return eventsOutput{}, err
		}
		aggregate := applyEventAggregateBudget(events, request.budget)
		reasons := []string(nil)
		if aggregate.partial {
			reasons = append(reasons, "aggregate_body_budget")
		}
		extent, extentErr := apptypes.NewEventPageExtent(
			len(events), len(aggregate.events), false, aggregate.partial, reasons,
		)
		if extentErr != nil {
			return eventsOutput{}, xerrors.Errorf("failed to resolve event page extent: %w", extentErr)
		}
		coverageBytes := 0
		for _, event := range aggregate.events {
			coverageBytes += encodedEventBodyBytes(event)
		}
		result := eventsOutput{Events: aggregate.events, Interval: interval, Coverage: eventCoverageOutput{CandidateCount: extent.CandidateCount(), ReturnedCount: extent.ReturnedCount(), AggregateBodyBytes: coverageBytes, AggregateBodyBudget: request.budget.AggregateBodyBytes()}, Partial: extent.Partial(), Reasons: extent.Reasons()}
		if aggregate.hasUnreturned {
			if len(aggregate.events) == 0 {
				return eventsOutput{}, xerrors.Errorf("cannot continue an empty event page")
			}
			anchor, anchorErr := eventPageAnchorFromOutput(aggregate.events[len(aggregate.events)-1])
			if anchorErr != nil {
				return eventsOutput{}, anchorErr
			}
			continuation, encodeErr := encodeEventContinuation(request, anchor, snapshot)
			if encodeErr != nil {
				return eventsOutput{}, encodeErr
			}
			result.Continuation = continuation
		}
		return result, nil
	}
	candidates, err := loaders.candidates(ctx, request.budget.ItemLimit()+1, request.offset)
	if err != nil {
		return eventsOutput{}, err
	}
	returnedCandidates := candidates
	hasMore := len(candidates) > request.budget.ItemLimit()
	if hasMore {
		returnedCandidates = candidates[:request.budget.ItemLimit()]
	}

	var events []eventOutput
	switch projection {
	case apptypes.EventProjectionMetadata:
		events = convertEventMetadata(returnedCandidates)
	case apptypes.EventProjectionBounded:
		if loaders.bounded == nil {
			return eventsOutput{}, xerrors.Errorf("event bounded usecase is not configured")
		}
		bounded, loadErr := loaders.bounded(ctx, returnedCandidates, request.budget.BodyRuneLimit())
		if loadErr != nil {
			return eventsOutput{}, loadErr
		}
		events = convertBoundedEvents(bounded)
	case apptypes.EventProjectionFull:
		if loaders.full == nil || loaders.convertFull == nil {
			return eventsOutput{}, xerrors.Errorf("event usecase is not configured")
		}
		full, loadErr := loaders.full(ctx, len(returnedCandidates), request.offset)
		if loadErr != nil {
			return eventsOutput{}, loadErr
		}
		events = loaders.convertFull(full)
	default:
		return eventsOutput{}, xerrors.Errorf("unsupported resolved event projection %q", projection)
	}
	aggregate := applyEventAggregateBudget(events, request.budget)
	reasons := make([]string, 0, 2)
	if hasMore {
		reasons = append(reasons, "more_results")
	}
	if aggregate.partial {
		reasons = append(reasons, "aggregate_body_budget")
	}
	extent, err := apptypes.NewEventPageExtent(
		len(candidates),
		len(aggregate.events),
		hasMore,
		hasMore || aggregate.partial,
		reasons,
	)
	if err != nil {
		return eventsOutput{}, xerrors.Errorf("failed to resolve event page extent: %w", err)
	}
	coverageBytes := 0
	for _, event := range aggregate.events {
		coverageBytes += encodedEventBodyBytes(event)
	}
	result := eventsOutput{
		Events: aggregate.events, Interval: interval,
		Coverage: eventCoverageOutput{CandidateCount: extent.CandidateCount(), ReturnedCount: extent.ReturnedCount(), AggregateBodyBytes: coverageBytes, AggregateBodyBudget: request.budget.AggregateBodyBytes()},
		Partial:  extent.Partial(), Reasons: extent.Reasons(),
	}
	if hasMore || aggregate.hasUnreturned {
		if len(aggregate.events) == 0 {
			return eventsOutput{}, xerrors.Errorf("cannot continue an empty event page")
		}
		anchor, anchorErr := eventPageAnchorFromOutput(aggregate.events[len(aggregate.events)-1])
		if anchorErr != nil {
			return eventsOutput{}, anchorErr
		}
		continuation, encodeErr := encodeEventContinuation(request, anchor, snapshot)
		if encodeErr != nil {
			return eventsOutput{}, encodeErr
		}
		result.Continuation = continuation
	}
	return result, nil
}
