package model_test

import (
	"testing"
	"time"

	"github.com/duck8823/traceary/domain/model"
	"github.com/duck8823/traceary/domain/types"
)

func TestNewWorkspaceAlias_ValidatesAndNormalizesReview(t *testing.T) {
	t.Parallel()
	reviewedAt := time.Date(2026, 7, 22, 1, 2, 3, 0, time.FixedZone("test", 3600))
	alias, err := model.NewWorkspaceAlias(types.SessionID("session-1"), types.Workspace(" /repo/worktree "), reviewedAt, " operator ", " reviewed checkout ")
	if err != nil {
		t.Fatalf("NewWorkspaceAlias() error = %v", err)
	}
	if alias.SessionID().String() != "session-1" || alias.Workspace().String() != "/repo/worktree" {
		t.Fatalf("identity = %q/%q", alias.SessionID(), alias.Workspace())
	}
	if alias.ReviewedBy() != "operator" || alias.Note() != "reviewed checkout" || alias.ReviewedAt().Location() != time.UTC {
		t.Fatalf("review = %q/%q/%s", alias.ReviewedBy(), alias.Note(), alias.ReviewedAt())
	}
}

func TestNewWorkspaceAlias_RejectsIncompleteReview(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		sessionID  types.SessionID
		workspace  types.Workspace
		reviewedAt time.Time
		reviewer   string
	}{
		"session":   {"", "/repo", time.Now(), "operator"},
		"workspace": {"session-1", " ", time.Now(), "operator"},
		"time":      {"session-1", "/repo", time.Time{}, "operator"},
		"reviewer":  {"session-1", "/repo", time.Now(), " "},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := model.NewWorkspaceAlias(tc.sessionID, tc.workspace, tc.reviewedAt, tc.reviewer, ""); err == nil {
				t.Fatal("NewWorkspaceAlias() error = nil")
			}
		})
	}
}
