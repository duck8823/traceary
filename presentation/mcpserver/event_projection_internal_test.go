package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestResolveEventProjection(t *testing.T) {
	t.Parallel()

	zero := 0
	negative := -1
	positive := 123
	for _, tt := range []struct {
		name       string
		projection string
		bodyLimit  *int
		fullBody   bool
		want       apptypes.EventProjection
		wantLimit  int
		wantErr    bool
	}{
		{name: "legacy omitted is bounded", want: apptypes.EventProjectionBounded, wantLimit: defaultListEventBodyLimit},
		{name: "legacy explicit zero is full", bodyLimit: &zero, want: apptypes.EventProjectionFull},
		{name: "legacy full_body is full", fullBody: true, want: apptypes.EventProjectionFull},
		{name: "legacy positive is bounded", bodyLimit: &positive, want: apptypes.EventProjectionBounded, wantLimit: positive},
		{name: "negative body limit is rejected", bodyLimit: &negative, wantErr: true},
		{name: "metadata", projection: "metadata", want: apptypes.EventProjectionMetadata},
		{name: "metadata ignores zero", projection: "metadata", bodyLimit: &zero, want: apptypes.EventProjectionMetadata},
		{name: "metadata rejects positive", projection: "metadata", bodyLimit: &positive, wantErr: true},
		{name: "metadata rejects full_body", projection: "metadata", fullBody: true, wantErr: true},
		{name: "bounded default", projection: "bounded", want: apptypes.EventProjectionBounded, wantLimit: defaultListEventBodyLimit},
		{name: "bounded rejects zero", projection: "bounded", bodyLimit: &zero, wantErr: true},
		{name: "full", projection: "full", want: apptypes.EventProjectionFull},
		{name: "full rejects positive", projection: "full", bodyLimit: &positive, wantErr: true},
		{name: "unknown", projection: "summary", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, gotLimit, err := resolveEventProjection(tt.projection, tt.bodyLimit, tt.fullBody)
			if (err != nil) != tt.wantErr {
				t.Fatalf("resolveEventProjection() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && (got != tt.want || gotLimit != tt.wantLimit) {
				t.Fatalf("resolveEventProjection() = (%q, %d), want (%q, %d)", got, gotLimit, tt.want, tt.wantLimit)
			}
		})
	}
}

func TestConvertEvents_reportsRetentionUnavailableBody(t *testing.T) {
	t.Parallel()

	eventID, err := types.EventIDFrom("retained-event")
	if err != nil {
		t.Fatalf("EventIDFrom() error = %v", err)
	}
	agent, err := types.AgentFrom("codex")
	if err != nil {
		t.Fatalf("AgentFrom() error = %v", err)
	}
	sessionID, err := types.SessionIDFrom("session-1")
	if err != nil {
		t.Fatalf("SessionIDFrom() error = %v", err)
	}
	event := model.EventOfWithBodyAvailabilityAndSourceHook(
		eventID, types.EventKindNote, "cli", agent, sessionID, "repo", "",
		types.BodyAvailabilityUnavailableRetention, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), "",
	)
	output := convertEventsWithBodyLimit([]*model.Event{event}, 100)
	if len(output) != 1 || output[0].Body != nil || output[0].BodyUnavailableReason != "retention" {
		t.Fatalf("retention output = %+v", output)
	}
}

func TestListEvents_MetadataProjectionOmitsBodyAndFullQuery(t *testing.T) {
	t.Parallel()

	metadata := newMCPMetadataFixture(t)
	full := &projectionEventUsecaseStub{}
	metadataUsecase := &projectionMetadataUsecaseStub{list: []apptypes.EventMetadata{metadata}}
	server := &Server{event: full, eventMetadata: metadataUsecase}
	_, output, err := server.listEvents()(context.Background(), nil, listEventsInput{Projection: "metadata", Limit: 1})
	if err != nil {
		t.Fatalf("listEvents() error = %v", err)
	}
	if full.listCalls != 0 || metadataUsecase.listCalls != 1 {
		t.Fatalf("full List() calls = %d, metadata List() calls = %d", full.listCalls, metadataUsecase.listCalls)
	}
	if len(output.Events) != 1 || output.Events[0].Body != nil || len(output.Events[0].BodyBlocks) != 0 {
		t.Fatalf("metadata event output = %+v", output.Events)
	}
	encoded, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var document struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	for _, forbidden := range []string{"body", "body_blocks", "body_truncated", "body_length"} {
		if _, ok := document.Events[0][forbidden]; ok {
			t.Fatalf("metadata output serialized %q: %s", forbidden, encoded)
		}
	}
	if document.Events[0]["body_stored_bytes"] != float64(8*1024*1024) {
		t.Fatalf("metadata output = %s", encoded)
	}
}

func TestSearchAndContext_MetadataProjectionUseBodyFreeQueries(t *testing.T) {
	t.Parallel()

	metadata := newMCPMetadataFixture(t)
	full := &projectionEventUsecaseStub{}
	metadataUsecase := &projectionMetadataUsecaseStub{
		search:  []apptypes.EventMetadata{metadata},
		context: []apptypes.EventMetadata{metadata},
	}
	server := &Server{event: full, eventMetadata: metadataUsecase}

	_, searchOutput, err := server.search()(context.Background(), nil, searchInput{Projection: "metadata", Query: "needle"})
	if err != nil {
		t.Fatalf("search() error = %v", err)
	}
	_, contextOutput, err := server.getContext()(context.Background(), nil, getContextInput{Projection: "metadata", SessionID: "session-1"})
	if err != nil {
		t.Fatalf("getContext() error = %v", err)
	}
	if full.searchCalls != 0 || full.contextCalls != 0 {
		t.Fatalf("full calls: search=%d context=%d, want 0", full.searchCalls, full.contextCalls)
	}
	if metadataUsecase.searchCalls != 1 || metadataUsecase.contextCalls != 1 {
		t.Fatalf("metadata calls: search=%d context=%d, want 1 each", metadataUsecase.searchCalls, metadataUsecase.contextCalls)
	}
	for name, events := range map[string][]eventOutput{"search": searchOutput.Events, "context": contextOutput.Events} {
		if len(events) != 1 || events[0].Body != nil || len(events[0].BodyBlocks) != 0 {
			t.Fatalf("%s metadata output = %+v", name, events)
		}
	}
}

func TestListEventsAndSearchShareRequestedIntervalSemantics(t *testing.T) {
	t.Parallel()

	wantFrom := time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 3, 9, 4, 0, 0, 0, time.UTC)

	t.Run("list events", func(t *testing.T) {
		t.Parallel()
		events := &projectionEventUsecaseStub{}
		server := &Server{event: events}
		_, output, err := server.listEvents()(context.Background(), nil, listEventsInput{
			From: "2026-03-08", To: "2026-03-08", Timezone: "America/New_York",
		})
		if err != nil {
			t.Fatalf("listEvents() error = %v", err)
		}
		assertMCPInterval(t, events.listCriteria.From(), events.listCriteria.To(), output.Interval, wantFrom, wantTo)
	})

	t.Run("search", func(t *testing.T) {
		t.Parallel()
		events := &projectionEventUsecaseStub{}
		server := &Server{event: events}
		_, output, err := server.search()(context.Background(), nil, searchInput{
			Query: "needle", From: "2026-03-08", To: "2026-03-08", Timezone: "America/New_York",
		})
		if err != nil {
			t.Fatalf("search() error = %v", err)
		}
		assertMCPInterval(t, events.searchCriteria.From(), events.searchCriteria.To(), output.Interval, wantFrom, wantTo)
	})
}

func assertMCPInterval(t *testing.T, gotFrom, gotTo time.Time, metadata *intervalOutput, wantFrom, wantTo time.Time) {
	t.Helper()
	if !gotFrom.Equal(wantFrom) || !gotTo.Equal(wantTo) {
		t.Fatalf("criteria interval = [%s, %s), want [%s, %s)", gotFrom, gotTo, wantFrom, wantTo)
	}
	if metadata == nil {
		t.Fatal("interval metadata = nil")
	}
	if metadata.RequestedFrom != "2026-03-08" || metadata.RequestedTo != "2026-03-08" || metadata.Timezone != "America/New_York" {
		t.Fatalf("interval metadata = %+v", metadata)
	}
	if metadata.EffectiveFromInclusive != wantFrom.Format(time.RFC3339Nano) || metadata.EffectiveToExclusive != wantTo.Format(time.RFC3339Nano) || metadata.SnapshotAt == "" {
		t.Fatalf("interval metadata = %+v", metadata)
	}
}

func TestListEvents_LegacyBodyControlsRemainCompatible(t *testing.T) {
	t.Parallel()

	body := strings.Repeat("stored-body-", 100)
	event := model.EventOf(
		types.EventID("event-full"), types.EventKindNote, types.Client("hook"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("duck8823/traceary"), body,
		time.Date(2026, 7, 22, 7, 30, 0, 0, time.UTC),
	)
	zero := 0
	for _, tt := range []struct {
		name          string
		bodyLimit     *int
		fullBody      bool
		wantTruncated bool
	}{
		{name: "omitted body limit is bounded", wantTruncated: true},
		{name: "explicit body_limit zero", bodyLimit: &zero},
		{name: "full_body true", fullBody: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			full := &projectionEventUsecaseStub{list: []*model.Event{event}}
			server := &Server{event: full}
			_, output, err := server.listEvents()(context.Background(), nil, listEventsInput{BodyLimit: tt.bodyLimit, FullBody: tt.fullBody})
			if err != nil {
				t.Fatalf("listEvents() error = %v", err)
			}
			if len(output.Events) != 1 || output.Events[0].Body == nil {
				t.Fatalf("listEvents() output = %+v", output.Events)
			}
			if output.Events[0].BodyTruncated != tt.wantTruncated {
				t.Fatalf("BodyTruncated = %v, want %v", output.Events[0].BodyTruncated, tt.wantTruncated)
			}
			if !tt.wantTruncated && *output.Events[0].Body != body {
				t.Fatalf("full body length = %d, want %d", len(*output.Events[0].Body), len(body))
			}
			if tt.wantTruncated && len([]rune(*output.Events[0].Body)) > defaultListEventBodyLimit+1 {
				t.Fatalf("bounded body runes = %d, want <= %d", len([]rune(*output.Events[0].Body)), defaultListEventBodyLimit+1)
			}
		})
	}
}

type projectionEventUsecaseStub struct {
	listCalls      int
	searchCalls    int
	contextCalls   int
	list           []*model.Event
	listCriteria   apptypes.EventListCriteria
	searchCriteria apptypes.EventSearchCriteria
}

func (*projectionEventUsecaseStub) Log(context.Context, string, types.EventKind, types.Client, types.Agent, types.SessionID, types.Workspace, apptypes.LogRedaction) (*model.Event, error) {
	return nil, nil
}
func (*projectionEventUsecaseStub) Audit(context.Context, apptypes.AuditInput, apptypes.AuditRedaction) (*model.Event, *model.CommandAudit, error) {
	return nil, nil, nil
}
func (s *projectionEventUsecaseStub) Search(_ context.Context, criteria apptypes.EventSearchCriteria) ([]*model.Event, error) {
	s.searchCalls++
	s.searchCriteria = criteria
	return nil, nil
}
func (s *projectionEventUsecaseStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]*model.Event, error) {
	s.listCalls++
	s.listCriteria = criteria
	return s.list, nil
}
func (*projectionEventUsecaseStub) ListWindow(context.Context, apptypes.EventListCriteria) ([]*model.Event, error) {
	return nil, nil
}
func (*projectionEventUsecaseStub) Show(context.Context, types.EventID) (apptypes.EventDetails, error) {
	return apptypes.EventDetails{}, nil
}
func (s *projectionEventUsecaseStub) Context(context.Context, apptypes.EventContextCriteria) ([]*model.Event, error) {
	s.contextCalls++
	return nil, nil
}
func (*projectionEventUsecaseStub) Timeline(context.Context, apptypes.TimelineCriteria) ([]apptypes.TimelineBlock, error) {
	return nil, nil
}

type projectionMetadataUsecaseStub struct {
	list         []apptypes.EventMetadata
	search       []apptypes.EventMetadata
	context      []apptypes.EventMetadata
	listCalls    int
	searchCalls  int
	contextCalls int
}

func (s *projectionMetadataUsecaseStub) List(context.Context, apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	s.listCalls++
	return s.list, nil
}
func (s *projectionMetadataUsecaseStub) Search(context.Context, apptypes.EventSearchCriteria) ([]apptypes.EventMetadata, error) {
	s.searchCalls++
	return s.search, nil
}
func (s *projectionMetadataUsecaseStub) Context(context.Context, apptypes.EventContextCriteria) ([]apptypes.EventMetadata, error) {
	s.contextCalls++
	return s.context, nil
}

func newMCPMetadataFixture(t *testing.T) apptypes.EventMetadata {
	t.Helper()
	extent, err := apptypes.EventBodyExtentOf(types.None[int](), 8*1024*1024, types.None[bool](), types.None[bool](), types.None[int]())
	if err != nil {
		t.Fatalf("EventBodyExtentOf() error = %v", err)
	}
	metadata, err := apptypes.EventMetadataOf(
		types.EventID("event-1"), types.EventKindCommandExecuted, types.Client("hook"), types.Agent("codex"),
		types.SessionID("session-1"), types.Workspace("duck8823/traceary"), "post_tool_use",
		time.Date(2026, 7, 22, 7, 0, 0, 0, time.UTC), extent,
		types.Some(apptypes.CommandAuditMetadataOf(types.Some(9), true)),
	)
	if err != nil {
		t.Fatalf("EventMetadataOf() error = %v", err)
	}
	return metadata
}
