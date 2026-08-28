package cli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/duck8823/traceary/application/queryservice"
)

type conversionSinceStub struct {
	rows    []queryservice.ConsolidationConversionRow
	auth    []queryservice.RefinementAuthorshipRow
	convErr error
	authErr error
}

func (s *conversionSinceStub) ConversionSince(context.Context, time.Time) ([]queryservice.ConsolidationConversionRow, error) {
	return s.rows, s.convErr
}

func (s *conversionSinceStub) RefinementAuthorshipSince(context.Context, time.Time) ([]queryservice.RefinementAuthorshipRow, error) {
	return s.auth, s.authErr
}

func TestInspectConsolidationConversionRendering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		qs         queryservice.ConsolidationConversionQueryService
		wantStatus string
		wantSub    []string
		wantHint   bool
	}{
		{
			name:       "nil query service skips",
			wantStatus: doctorStatusSkip,
			wantSub:    []string{"not configured"},
		},
		{
			name:       "query error warns",
			qs:         &conversionSinceStub{convErr: errors.New("locked")},
			wantStatus: doctorStatusWarn,
			wantSub:    []string{"failed to read consolidation conversion"},
		},
		{
			name:       "no rows pass",
			qs:         &conversionSinceStub{},
			wantStatus: doctorStatusPass,
			wantSub:    []string{"no consolidation requests in the last 7 days"},
		},
		{
			name: "4 requests 0 refined passes below min",
			qs: &conversionSinceStub{rows: []queryservice.ConsolidationConversionRow{{
				Client: "claude", Requests: 4, SessionsRequested: 4,
			}}},
			wantStatus: doctorStatusPass,
			wantSub:    []string{"claude: 4 requests / 4 sessions asked / 0 sessions refined (0%)"},
		},
		{
			name: "5 requests 1 refined warns below 25 percent",
			qs: &conversionSinceStub{rows: []queryservice.ConsolidationConversionRow{{
				Client: "claude", Requests: 5, SessionsRequested: 5, SessionsRefined: 1,
			}}},
			wantStatus: doctorStatusWarn,
			wantSub:    []string{"claude: 5 requests / 5 sessions asked / 1 sessions refined (20%)"},
			wantHint:   true,
		},
		{
			name: "5 requests 2 refined passes at 40 percent",
			qs: &conversionSinceStub{rows: []queryservice.ConsolidationConversionRow{{
				Client: "claude", Requests: 5, SessionsRequested: 5, SessionsRefined: 2,
			}}},
			wantStatus: doctorStatusPass,
			wantSub:    []string{"claude: 5 requests / 5 sessions asked / 2 sessions refined (40%)"},
		},
		{
			name: "per-client clauses are ordered by client name",
			qs: &conversionSinceStub{rows: []queryservice.ConsolidationConversionRow{
				{Client: "claude", Requests: 1, SessionsRequested: 1, SessionsRefined: 1},
				{Client: "codex", Requests: 2, SessionsRequested: 2, SessionsRefined: 2},
			}},
			wantStatus: doctorStatusPass,
			wantSub: []string{
				"claude: 1 requests / 1 sessions asked / 1 sessions refined (100%)",
				"codex: 2 requests / 2 sessions asked / 2 sessions refined (100%)",
			},
		},
		{
			name: "zero sessions requested does not divide by zero",
			qs: &conversionSinceStub{rows: []queryservice.ConsolidationConversionRow{{
				Client: "claude", Requests: 2, SessionsRequested: 0,
			}}},
			wantStatus: doctorStatusPass,
			wantSub:    []string{"claude: 2 requests / 0 sessions asked / 0 sessions refined (0%)"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var root *RootCLI
			if tt.qs == nil {
				root = NewRootCLI()
			} else {
				root = NewRootCLI(WithConsolidationConversion(tt.qs))
			}
			check := root.inspectConsolidationConversion(context.Background())
			if check.Name != "consolidation-conversion" {
				t.Fatalf("name = %q", check.Name)
			}
			if check.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s; message=%q", check.Status, tt.wantStatus, check.Message)
			}
			for _, sub := range tt.wantSub {
				if !strings.Contains(check.Message, sub) {
					t.Fatalf("message %q does not contain %q", check.Message, sub)
				}
			}
			if tt.wantHint && check.Hint == "" {
				t.Fatal("hint is empty, want WARN hint")
			}
			if !tt.wantHint && check.Hint != "" {
				t.Fatalf("hint = %q, want empty", check.Hint)
			}
		})
	}
}
