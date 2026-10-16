package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/application/usecase"
	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

// This fake models the existing descending metadata read contract. Payload reads
// fail separately so the test observes data access, not merely omitted JSON keys.
type tailMetadataUsecaseStub struct {
	usecase.EventMetadataUsecase
	initial     []apptypes.EventMetadata
	windows     [][]apptypes.EventMetadata
	listCalls   []apptypes.EventListCriteria
	windowCalls []apptypes.EventListCriteria
	listErr     error
	windowErr   error
	onWindow    func(int)
}

func (s *tailMetadataUsecaseStub) List(_ context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	s.listCalls = append(s.listCalls, criteria)
	return s.initial, s.listErr
}

func (s *tailMetadataUsecaseStub) ListWindow(_ context.Context, criteria apptypes.EventListCriteria) ([]apptypes.EventMetadata, error) {
	index := len(s.windowCalls)
	s.windowCalls = append(s.windowCalls, criteria)
	if s.onWindow != nil {
		s.onWindow(index)
	}
	if s.windowErr != nil {
		return nil, s.windowErr
	}
	if index < len(s.windows) {
		return s.windows[index], nil
	}
	return nil, nil
}

func tailMetadataFixture(t *testing.T, id, session string, at time.Time, exitCode types.Optional[int]) apptypes.EventMetadata {
	t.Helper()
	row, err := apptypes.EventMetadataOf(types.EventID(id), types.EventKindCommandExecuted, "cli", "codex", types.SessionID(session), "duck8823/traceary", "post_tool_use", at, apptypes.EventBodyExtent{}, types.Some(apptypes.CommandAuditMetadataOf(exitCode, false)))
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestRunTail_MetadataOnlyInitialAndEveryPoll(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	first := tailMetadataFixture(t, "event-1", "session-1", at, types.Some(17))
	second := tailMetadataFixture(t, "event-2", "session-1", at, types.None[int]())
	third := tailMetadataFixture(t, "event-3", "session-1", at.Add(time.Second), types.Some(0))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	metadata := &tailMetadataUsecaseStub{
		initial: []apptypes.EventMetadata{first},
		windows: [][]apptypes.EventMetadata{{second, first}, {third, second, first}},
		onWindow: func(index int) {
			if index == 1 {
				cancel()
			}
		},
	}
	forbidden := errors.New("payload access forbidden")
	payload := &tailEventUsecaseStub{listErrs: []error{forbidden}, listWindowErrs: []error{forbidden}, hydrateErr: forbidden, showErr: forbidden}
	ticker := newFakeTailTicker()
	ticker.ch <- at
	ticker.ch <- at
	var output bytes.Buffer
	sut := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(payload), WithEventMetadata(metadata))
	err := sut.runTail(ctx, &bytes.Buffer{}, &output, tailCommandInput{
		dbPath: "/tmp/test-traceary.db", limit: 1, repo: "duck8823/traceary", asJSON: true,
		fieldsSet: true, fields: []string{"id", "ts", "kind", "session", "ws", "client", "agent", "source_hook", "exit_code"},
		nowFunc: func() time.Time { return at.Add(time.Minute) }, tickerFactory: func(time.Duration) tailTicker { return ticker },
	})
	if err != nil {
		t.Fatalf("runTail() error = %v", err)
	}
	if len(payload.listCalls)+len(payload.listWindowCalls)+payload.hydrateCalls+payload.showCalls != 0 {
		t.Fatal("metadata-only follow accessed payloads")
	}
	if len(metadata.listCalls) != 1 || len(metadata.windowCalls) != 2 {
		t.Fatalf("metadata calls = %d / %d", len(metadata.listCalls), len(metadata.windowCalls))
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("NDJSON rows = %d, want 3: %s", len(lines), output.String())
	}
	for index, line := range lines {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		if len(row) != 9 || row["event_id"] != fmt.Sprintf("event-%d", index+1) || row["session_id"] != "session-1" || row["source_hook"] != "post_tool_use" {
			t.Fatalf("row = %#v", row)
		}
		wantExit := []any{float64(17), nil, float64(0)}[index]
		if row["exit_code"] != wantExit {
			t.Fatalf("exit_code = %v, want %v", row["exit_code"], wantExit)
		}
		if _, found := row["message"]; found {
			t.Fatal("metadata contains message")
		}
	}
	for _, criteria := range metadata.windowCalls {
		if !criteria.From().Equal(at) || !criteria.To().Equal(at.Add(time.Minute)) || criteria.Limit() != defaultTailBatchSize || criteria.Offset() != 0 {
			t.Fatalf("unexpected poll window: %#v", criteria)
		}
	}
}

func TestRunTail_MetadataOnlySessionFilterAdvancesHiddenRowsAndDrainsWindow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	hiddenInitial := tailMetadataFixture(t, "hidden-initial", "hidden-session", at, types.None[int]())
	hiddenPoll := tailMetadataFixture(t, "hidden-poll", "hidden-session", at.Add(time.Second), types.None[int]())
	const count = defaultTailBatchSize*2 + 5
	window := make([]apptypes.EventMetadata, 0, count+1)
	for i := count; i > 0; i-- {
		window = append(window, tailMetadataFixture(t, fmt.Sprintf("event-%03d", i), "matching-session", at.Add(time.Duration(i+1)*time.Second), types.None[int]()))
	}
	window = append(window, hiddenPoll)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	metadata := &tailMetadataUsecaseStub{
		initial: []apptypes.EventMetadata{hiddenInitial},
		windows: [][]apptypes.EventMetadata{{hiddenPoll, hiddenInitial}, window},
		onWindow: func(index int) {
			if index == 1 {
				cancel()
			}
		},
	}
	ticker := newFakeTailTicker()
	ticker.ch <- at
	ticker.ch <- at
	var output bytes.Buffer
	sut := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(&tailEventUsecaseStub{}), WithEventMetadata(metadata))
	if err := sut.runTail(ctx, io.Discard, &output, tailCommandInput{
		dbPath: "/tmp/test-traceary.db", limit: 1, asJSON: true, fieldsSet: true, fields: []string{"id"}, followSession: "matching",
		kind: "audit", client: "cli", agent: "codex", sessionID: "session-filter", repo: "duck8823/traceary", failuresOnly: true,
		nowFunc: func() time.Time { return at.Add(time.Hour) }, tickerFactory: func(time.Duration) tailTicker { return ticker },
	}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != count {
		t.Fatalf("row count = %d, want %d", len(lines), count)
	}
	for i, line := range lines {
		if want := fmt.Sprintf(`{"event_id":"event-%03d"}`, i+1); line != want {
			t.Fatalf("row %d = %s, want %s", i, line, want)
		}
	}
	if len(metadata.windowCalls) != 2 || !metadata.windowCalls[1].From().Equal(hiddenPoll.CreatedAt()) {
		t.Fatalf("hidden poll did not advance cursor: %#v", metadata.windowCalls)
	}
	for _, criteria := range append(metadata.listCalls, metadata.windowCalls...) {
		if criteria.Kind() != types.EventKindCommandExecuted || criteria.Client() != "cli" || criteria.Agent() != "codex" || criteria.SessionID() != "session-filter" || criteria.Workspace() != "duck8823/traceary" || !criteria.FailuresOnly() {
			t.Fatalf("lost scope: %#v", criteria)
		}
	}
}

func TestRunTail_MetadataOnlyZeroLimitAndCancellation(t *testing.T) {
	t.Parallel()
	for _, idle := range []bool{false, true} {
		t.Run(fmt.Sprintf("idle=%v", idle), func(t *testing.T) {
			at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ticker := newFakeTailTicker()
			metadata := &tailMetadataUsecaseStub{
				listErr:  errors.New("initial fetch forbidden for limit 0"),
				windows:  [][]apptypes.EventMetadata{{tailMetadataFixture(t, "new", "session-1", at, types.None[int]())}},
				onWindow: func(int) { cancel() },
			}
			if idle {
				cancel()
			} else {
				ticker.ch <- at
			}
			var output bytes.Buffer
			sut := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(&tailEventUsecaseStub{}), WithEventMetadata(metadata))
			if err := sut.runTail(ctx, io.Discard, &output, tailCommandInput{
				dbPath: "/tmp/test-traceary.db", limit: 0, repo: "duck8823/traceary", asJSON: true, fieldsSet: true, fields: []string{"id"},
				nowFunc: func() time.Time { return at }, tickerFactory: func(time.Duration) tailTicker { return ticker },
			}); err != nil {
				t.Fatal(err)
			}
			if len(metadata.listCalls) != 0 {
				t.Fatal("limit 0 read initial history")
			}
			if idle {
				if len(metadata.windowCalls) != 0 || output.Len() != 0 {
					t.Fatal("canceled idle stream read or wrote events")
				}
			} else if len(metadata.windowCalls) != 1 || !metadata.windowCalls[0].From().Equal(at) || output.String() != "{\"event_id\":\"new\"}\n" {
				t.Fatalf("limit 0 output = %q, windows = %#v", output.String(), metadata.windowCalls)
			}
		})
	}
}

type tailBrokenPipeWriter struct{}

func (tailBrokenPipeWriter) Write([]byte) (int, error) { return 0, syscall.EPIPE }

func TestRunTail_MetadataOnlyPropagatesReadAndWriteErrors(t *testing.T) {
	t.Parallel()
	failure := errors.New("metadata query failed")
	for _, stage := range []string{"initial read", "poll read", "initial write", "poll write"} {
		t.Run(stage, func(t *testing.T) {
			at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
			metadata := &tailMetadataUsecaseStub{}
			limit := 1
			output := io.Discard
			want := failure
			row := tailMetadataFixture(t, "event-1", "session-1", at, types.None[int]())
			switch stage {
			case "initial read":
				metadata.listErr = failure
			case "poll read":
				metadata.windowErr = failure
				limit = 0
			case "initial write":
				metadata.initial = []apptypes.EventMetadata{row}
				output = tailBrokenPipeWriter{}
				want = syscall.EPIPE
			case "poll write":
				metadata.windows = [][]apptypes.EventMetadata{{row}}
				limit = 0
				output = tailBrokenPipeWriter{}
				want = syscall.EPIPE
			}
			ticker := newFakeTailTicker()
			ticker.ch <- at
			sut := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(&tailEventUsecaseStub{}), WithEventMetadata(metadata))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := sut.runTail(ctx, io.Discard, output, tailCommandInput{
				dbPath: "/tmp/test-traceary.db", limit: limit, repo: "duck8823/traceary", asJSON: true, fieldsSet: true, fields: []string{"id"},
				nowFunc: func() time.Time { return at }, tickerFactory: func(time.Duration) tailTicker { return ticker },
			})
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

func TestRunTail_MessageAndDefaultJSONKeepPayloadReads(t *testing.T) {
	t.Parallel()
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprintf("explicit=%v", explicit), func(t *testing.T) {
			at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			initial := mustTailEvent(t, "event-initial", "cli", "codex", "session-1", "duck8823/traceary", "initial body", at)
			follow := mustTailEvent(t, "event-follow", "cli", "codex", "session-1", "duck8823/traceary", "follow body", at.Add(time.Second))
			payload := &tailEventUsecaseStub{listResponses: [][]*model.Event{{initial}}, listWindowResponses: [][]*model.Event{{follow, initial}}, onListWindow: func(int, apptypes.EventListCriteria) { cancel() }}
			metadata := &tailMetadataUsecaseStub{listErr: errors.New("unexpected metadata read"), windowErr: errors.New("unexpected metadata read")}
			ticker := newFakeTailTicker()
			ticker.ch <- at
			var output bytes.Buffer
			sut := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(payload), WithEventMetadata(metadata))
			if err := sut.runTail(ctx, io.Discard, &output, tailCommandInput{
				dbPath: "/tmp/test-traceary.db", limit: 1, repo: "duck8823/traceary", asJSON: true, fieldsSet: explicit, fields: []string{"id", "message"},
				nowFunc: func() time.Time { return at.Add(time.Minute) }, tickerFactory: func(time.Duration) tailTicker { return ticker },
			}); err != nil {
				t.Fatal(err)
			}
			if len(payload.listCalls) != 1 || len(payload.listWindowCalls) != 1 || payload.hydrateCalls != 2 || len(metadata.listCalls)+len(metadata.windowCalls) != 0 {
				t.Fatal("payload route changed")
			}
			if !strings.Contains(output.String(), `"message":"initial body"`) || !strings.Contains(output.String(), `"message":"follow body"`) {
				t.Fatalf("missing body: %s", output.String())
			}
		})
	}
}

func TestListFollow_MetadataOnlyInitialNDJSON(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // The initial read still runs; then follow exits without a real tick.
	metadata := &tailMetadataUsecaseStub{initial: []apptypes.EventMetadata{tailMetadataFixture(t, "event-1", "session-1", at, types.Some(17))}}
	payload := &tailEventUsecaseStub{listErrs: []error{errors.New("payload read forbidden")}}
	command := NewRootCLI(WithStoreManagement(tailStoreManagementStub{}), WithEvent(payload), WithEventMetadata(metadata)).Command()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"list", "--follow", "--json", "--fields", "id,exit_code", "--db-path", "/tmp/test-traceary.db", "--workspace", "duck8823/traceary", "--limit", "1"})
	if err := command.ExecuteContext(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "{\"event_id\":\"event-1\",\"exit_code\":17}\n"; output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
	if len(metadata.listCalls) != 1 || len(metadata.windowCalls) != 0 || len(payload.listCalls)+payload.hydrateCalls+payload.showCalls != 0 {
		t.Fatal("list follow did not retain metadata projection")
	}
}
