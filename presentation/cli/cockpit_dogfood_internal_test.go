package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	apptypes "github.com/duck8823/traceary/application/types"
	"github.com/duck8823/traceary/domain/model"
	domtypes "github.com/duck8823/traceary/domain/types"
	"github.com/duck8823/traceary/presentation/cli/tui"
)

type cockpitDogfoodSnapshotScenario struct {
	name  string
	model cockpitModel
}

func TestCockpitDogfoodGoldenSnapshots(t *testing.T) {
	previousTopNow := topNowFunc
	topNowFunc = func() time.Time { return time.Date(2026, 5, 9, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { topNowFunc = previousTopNow })

	for _, scenario := range cockpitDogfoodSnapshotScenarios(t) {
		scenario := scenario
		t.Run(scenario.name, func(t *testing.T) {
			assertCockpitDogfoodGolden(t, scenario.name, scenario.model.View())
		})
	}
}

func TestCockpitDogfoodJapaneseNarrowGoldenSnapshot(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "ja")

	model := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		DBPath:                  "/tmp/traceary.db",
		AcceptedMemoryCount:     3,
		CandidateMemoryCount:    4,
		NewCandidateMemoryKnown: true,
		NewCandidateMemoryCount: 2,
		MemoryLastSeenAt:        fixedStartedAt.Add(-2 * time.Hour),
		RememberIntentCount:     1,
		LowQualityMemoryCount:   1,
	})
	model.showHelp = true
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	assertCockpitDogfoodGolden(t, "home_candidate_memories_ja_narrow", updated.(cockpitModel).View())
}

func TestCockpitDogfoodTerminalSizesKeepTaskCues(t *testing.T) {
	t.Parallel()

	sizes := []struct {
		name   string
		width  int
		height int
	}{
		{name: "narrow", width: 80, height: 24},
		{name: "normal", width: 120, height: 32},
		{name: "wide", width: 160, height: 40},
	}
	expectations := map[string][]string{
		"home_all_green": {
			"ALL GREEN",
			"2 Live for ongoing work",
			"4 Memory for periodic review",
			"3 Doctor before release",
			"5 Sessions for handoff",
		},
		"home_doctor_failure": {
			"[FAIL] Doctor failures",
			"next: 3 Doctor shows remediation commands",
		},
		"home_candidate_memories": {
			"[NEW] Memory review",
			"new candidate memories=2",
			"edit/distill or skip",
		},
		"home_stale_sessions": {
			"[WARN] Stale active sessions",
			"traceary session gc --stale-after 24h --dry-run",
		},
		"home_new_events_and_failure": {
			"[WARN] Recent failures",
			"[NEW] Events",
			"2 Live opens the event stream",
		},
		"memory_ambiguous_candidate": {
			"accept requires confirmation",
			"e edit/distill",
			"EVIDENCE_REFS:          0",
		},
	}
	for _, scenario := range cockpitDogfoodSnapshotScenarios(t) {
		scenario := scenario
		for _, size := range sizes {
			size := size
			t.Run(scenario.name+"/"+size.name, func(t *testing.T) {
				t.Parallel()
				updated, _ := scenario.model.Update(tea.WindowSizeMsg{Width: size.width, Height: size.height})
				view := updated.(cockpitModel).View()
				for _, must := range expectations[scenario.name] {
					if !strings.Contains(view, must) {
						t.Fatalf("%s %dx%d missing %q:\n%s", scenario.name, size.width, size.height, must, view)
					}
				}
				for _, globalCue := range []string{"sections:", "quit"} {
					if !strings.Contains(view, globalCue) {
						t.Fatalf("%s %dx%d missing global cue %q:\n%s", scenario.name, size.width, size.height, globalCue, view)
					}
				}
				sizeCue := "terminal " + strconv.Itoa(size.width) + "x" + strconv.Itoa(size.height)
				if !strings.Contains(view, sizeCue) {
					t.Fatalf("%s %dx%d missing resize cue %q:\n%s", scenario.name, size.width, size.height, sizeCue, view)
				}
			})
		}
	}
}

func TestCockpitDogfoodKeyboardPaths(t *testing.T) {
	t.Parallel()

	t.Run("find latest failure from home", func(t *testing.T) {
		t.Parallel()
		failure := mustEvent(t, "evt-dogfood-failure", domtypes.EventKindCommandExecuted, "go test ./... failed")
		loader := &cockpitLoaderStub{
			liveResponses: []cockpitLiveSnapshot{{Events: []*model.Event{failure}, Cursor: newTailCursor(failure.CreatedAt()), LoadedAt: fixedStartedAt}},
			detailContent: topDetailContent{title: "EVENT evt-dogfood-failure", lines: []string{"exit_code=1", "go test ./... failed"}},
		}
		model := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, cockpitHomeSnapshot{LoadedAt: fixedStartedAt, RecentFailureCount: 1})
		model.loader = loader
		model.loaderCtx = t.Context()

		updated, cmd := model.Update(cockpitRuneKey("2"))
		model = updated.(cockpitModel)
		if model.mode != cockpitModeLive || cmd == nil {
			t.Fatalf("2 mode/cmd = %v/%T, want live/load", model.mode, cmd)
		}
		updated, _ = model.Update(cmd())
		model = updated.(cockpitModel)
		updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = updated.(cockpitModel)
		if model.mode != cockpitModeDetail || cmd == nil {
			t.Fatalf("enter mode/cmd = %v/%T, want detail/load", model.mode, cmd)
		}
		updated, _ = model.Update(cmd())
		model = updated.(cockpitModel)
		view := model.View()
		for _, must := range []string{"EVENT evt-dogfood-failure", "exit_code=1", "go test ./... failed"} {
			if !strings.Contains(view, must) {
				t.Fatalf("failure detail missing %q:\n%s", must, view)
			}
		}
	})

	t.Run("review ambiguous memory without accidental accept", func(t *testing.T) {
		t.Parallel()
		candidate := buildReviewCandidateWithOptions(t, reviewCandidateOptions{
			id:         "mem-dogfood-ambiguous",
			fact:       "Maybe the operator prefers short summaries",
			confidence: domtypes.ConfidenceLow,
			source:     domtypes.MemorySourceExtractedHidden,
			noEvidence: true,
		})
		loader := &cockpitLoaderStub{reviewItems: []apptypes.MemoryDetails{candidate}, reviewLoadStartedAt: fixedStartedAt}
		model := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, cockpitHomeSnapshot{LoadedAt: fixedStartedAt, CandidateMemoryCount: 1})
		model.loader = loader
		model.loaderCtx = t.Context()

		updated, cmd := model.Update(cockpitRuneKey("4"))
		model = updated.(cockpitModel)
		if model.mode != cockpitModeMemoryReview || cmd == nil {
			t.Fatalf("4 mode/cmd = %v/%T, want memory/load", model.mode, cmd)
		}
		updated, cmd = model.Update(cmd())
		model = updated.(cockpitModel)
		if cmd == nil {
			t.Fatalf("memory load should return mark-seen command")
		}
		if msg := cmd(); msg != nil {
			t.Fatalf("memory mark-seen command returned %T, want nil", msg)
		}

		updated, cmd = model.Update(cockpitRuneKey("v"))
		model = updated.(cockpitModel)
		if model.memoryReview.review.mode != reviewModeViewEvidence || cmd != nil {
			t.Fatalf("v mode/cmd = %v/%T, want evidence/nil", model.memoryReview.review.mode, cmd)
		}
		updated, cmd = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
		model = updated.(cockpitModel)
		if model.memoryReview.review.mode != reviewModeBrowse || cmd != nil {
			t.Fatalf("esc evidence mode/cmd = %v/%T, want browse/nil", model.memoryReview.review.mode, cmd)
		}
		updated, cmd = model.Update(cockpitRuneKey("a"))
		model = updated.(cockpitModel)
		if cmd != nil || len(model.memoryReview.review.Decisions()) != 0 {
			t.Fatalf("first accept should not queue ambiguous candidate, cmd=%T decisions=%+v", cmd, model.memoryReview.review.Decisions())
		}
		if !strings.Contains(model.View(), "accept confirmation armed") {
			t.Fatalf("ambiguous memory view missing confirmation guard:\n%s", model.View())
		}
	})

	t.Run("run doctor and find remediation", func(t *testing.T) {
		t.Parallel()
		loader := &cockpitLoaderStub{doctorResponses: []cockpitDoctorSnapshot{{
			LoadedAt: fixedStartedAt,
			Summary:  doctorSummary{Pass: 3, Warn: 1},
			Sections: []cockpitDoctorSection{{Name: "Hooks", Checks: []cockpitDoctorCheck{
				{Name: "codex-config", Status: doctorStatusWarn, Severity: doctorSeverityWarn, Message: "missing hook", FixCommand: "traceary hooks install --client codex"},
			}}},
		}}}
		model := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, cockpitHomeSnapshot{LoadedAt: fixedStartedAt, DoctorWarnCount: 1})
		model.loader = loader
		model.loaderCtx = t.Context()

		updated, cmd := model.Update(cockpitRuneKey("3"))
		model = updated.(cockpitModel)
		if model.mode != cockpitModeDoctor || cmd == nil {
			t.Fatalf("3 mode/cmd = %v/%T, want doctor/load", model.mode, cmd)
		}
		updated, _ = model.Update(cmd())
		model = updated.(cockpitModel)
		if view := model.View(); !strings.Contains(view, "traceary hooks install --client codex") {
			t.Fatalf("doctor dogfood view missing remediation command:\n%s", view)
		}
	})
}

func TestCockpitDogfoodJapaneseNarrowSmoke(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "ja")

	home := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		DBPath:                  "/tmp/traceary.db",
		AcceptedMemoryCount:     3,
		CandidateMemoryCount:    4,
		NewCandidateMemoryKnown: true,
		NewCandidateMemoryCount: 2,
		MemoryLastSeenAt:        fixedStartedAt.Add(-2 * time.Hour),
		RememberIntentCount:     1,
		LowQualityMemoryCount:   1,
	}
	model := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, home)
	model.showHelp = true
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view := updated.(cockpitModel).View()
	for _, must := range []string{
		"Traceary cockpit · ホーム",
		"セクション:",
		"トリアージ board",
		"メモリ inbox",
		"新着 candidate memory=2",
		"次:",
		"端末 80x24",
		"アクション menu",
		"Global navigation",
		"1 ホーム",
		"? ヘルプ",
	} {
		if !strings.Contains(view, must) {
			t.Fatalf("Japanese cockpit narrow smoke missing %q:\n%s", must, view)
		}
	}

	candidate := buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "mem-dogfood-ja-ambiguous",
		fact:       "Maybe the operator prefers short summaries",
		confidence: domtypes.ConfidenceLow,
		source:     domtypes.MemorySourceExtractedHidden,
		noEvidence: true,
	})
	memoryModel := newCockpitModel(tui.DefaultKeyMap(), tui.Styles{}, cockpitHomeSnapshot{LoadedAt: fixedStartedAt, CandidateMemoryCount: 1})
	memoryModel.mode = cockpitModeMemoryReview
	memoryModel.memoryReview.items = []apptypes.MemoryDetails{candidate}
	memoryModel.memoryReview.review = newReviewModel(memoryModel.memoryReview.items, memoryModel.keys, memoryModel.styles)
	updated, _ = memoryModel.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	memoryView := updated.(cockpitModel).View()
	for _, must := range []string{
		"Traceary cockpit · メモリ review",
		"判断カード",
		"判断 context",
		"信頼度が低い",
		"accept には確認が必要",
		"事実で安定している",
		"q 終了/適用",
	} {
		if !strings.Contains(memoryView, must) {
			t.Fatalf("Japanese memory review smoke missing %q:\n%s", must, memoryView)
		}
	}
}

func cockpitDogfoodSnapshotScenarios(t *testing.T) []cockpitDogfoodSnapshotScenario {
	t.Helper()
	styles := tui.Styles{}
	allGreen := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		DBPath:                  "/tmp/traceary.db",
		DoctorPassCount:         4,
		AcceptedMemoryCount:     2,
		NewCandidateMemoryKnown: true,
		MemoryLastSeenAt:        fixedStartedAt.Add(-time.Hour),
		NewEventKnown:           true,
		EventLastSeenAt:         fixedStartedAt.Add(-30 * time.Minute),
	}
	doctorFailure := cockpitHomeSnapshot{LoadedAt: fixedStartedAt, DBPath: "/tmp/traceary.db", DoctorPassCount: 2, DoctorWarnCount: 1, DoctorFailCount: 1, HookFailCount: 1}
	candidateMemories := cockpitHomeSnapshot{
		LoadedAt:                fixedStartedAt,
		DBPath:                  "/tmp/traceary.db",
		AcceptedMemoryCount:     3,
		CandidateMemoryCount:    4,
		NewCandidateMemoryKnown: true,
		NewCandidateMemoryCount: 2,
		MemoryLastSeenAt:        fixedStartedAt.Add(-2 * time.Hour),
		RememberIntentCount:     1,
		LowQualityMemoryCount:   1,
	}
	staleSessions := cockpitHomeSnapshot{LoadedAt: fixedStartedAt, DBPath: "/tmp/traceary.db", StaleActiveSessionCount: 2}
	newEventsAndFailure := cockpitHomeSnapshot{
		LoadedAt:           fixedStartedAt,
		DBPath:             "/tmp/traceary.db",
		NewEventKnown:      true,
		NewEventCount:      3,
		EventLastSeenAt:    fixedStartedAt.Add(-30 * time.Minute),
		RecentFailureCount: 1,
		RecentCommandCount: 2,
	}
	ambiguous := buildReviewCandidateWithOptions(t, reviewCandidateOptions{
		id:         "mem-dogfood-ambiguous",
		fact:       "Maybe the operator prefers short summaries",
		confidence: domtypes.ConfidenceLow,
		source:     domtypes.MemorySourceExtractedHidden,
		noEvidence: true,
	})
	memoryModel := newCockpitModel(tui.DefaultKeyMap(), styles, cockpitHomeSnapshot{LoadedAt: fixedStartedAt, CandidateMemoryCount: 1})
	memoryModel.mode = cockpitModeMemoryReview
	memoryModel.memoryReview.items = []apptypes.MemoryDetails{ambiguous}
	memoryModel.memoryReview.review = newReviewModel(memoryModel.memoryReview.items, memoryModel.keys, memoryModel.styles)
	return []cockpitDogfoodSnapshotScenario{
		{name: "home_all_green", model: newCockpitModel(tui.DefaultKeyMap(), styles, allGreen)},
		{name: "home_doctor_failure", model: newCockpitModel(tui.DefaultKeyMap(), styles, doctorFailure)},
		{name: "home_candidate_memories", model: newCockpitModel(tui.DefaultKeyMap(), styles, candidateMemories)},
		{name: "home_stale_sessions", model: newCockpitModel(tui.DefaultKeyMap(), styles, staleSessions)},
		{name: "home_new_events_and_failure", model: newCockpitModel(tui.DefaultKeyMap(), styles, newEventsAndFailure)},
		{name: "memory_ambiguous_candidate", model: memoryModel},
	}
}

func assertCockpitDogfoodGolden(t *testing.T, name string, got string) {
	t.Helper()
	goldenPath := filepath.Join("testdata", "cockpit", name+".golden.txt")
	got = strings.ReplaceAll(got, "\r\n", "\n")
	if !strings.HasSuffix(got, "\n") {
		got += "\n"
	}
	if os.Getenv("TRACEARY_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("MkdirAll golden dir: %v", err)
		}
		if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
			t.Fatalf("WriteFile golden: %v", err)
		}
	}
	wantBytes, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("ReadFile golden %s: %v", goldenPath, err)
	}
	want := strings.ReplaceAll(string(wantBytes), "\r\n", "\n")
	if got != want {
		t.Fatalf("cockpit dogfood golden %s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
