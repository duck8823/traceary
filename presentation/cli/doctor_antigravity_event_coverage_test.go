package cli

import (
	"strings"
	"testing"

	appusecase "github.com/duck8823/traceary/application/usecase"
)

func TestBuildAntigravityEventCoverageCheck(t *testing.T) {
	tests := []struct {
		name     string
		coverage appusecase.SessionEventCoverage
		want     string
	}{
		{name: "small sample passes without judging", coverage: appusecase.SessionEventCoverage{Sessions: doctorEventCoverageMinSample - 1}, want: doctorStatusPass},
		{name: "missing transcripts warn", coverage: appusecase.SessionEventCoverage{Sessions: 20, WithTranscript: 0, WithCommand: 8}, want: doctorStatusWarn},
		{name: "healthy transcript ratio passes", coverage: appusecase.SessionEventCoverage{Sessions: 20, WithTranscript: 19, WithCommand: 8}, want: doctorStatusPass},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			check := buildAntigravityEventCoverageCheck(100, tt.coverage, 0.10)
			if check.Status != tt.want {
				t.Fatalf("Status = %q, want %q (message=%q)", check.Status, tt.want, check.Message)
			}
			if !strings.Contains(check.Message, "transcript") {
				t.Fatalf("Message = %q, want transcript evidence", check.Message)
			}
		})
	}
}
