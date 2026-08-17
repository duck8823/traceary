package usecase

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestClassifyExtractionNoise_TransientStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		fact string
		want []string
	}{
		{
			name: "start work and children still open",
			fact: "計画と受け入れ条件を読み、v0.41 の作業を始めます。21 子はすべて OPEN です。",
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "wave merge and draft progress",
			fact: "Wave 1（#2010–#2013）は既に merge 済みです。Wave 2 は 5 本の Draft PR まで出しました。親 #2025 は OPEN のままです。",
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "remaining wave items in progress on worktree",
			fact: "残りの Wave 1 は #2010（cleanup のページングと park）と #2013（epoch-zero 修復）です。どちらも worktree 上で作業中で、まだ c",
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "status table row",
			fact: "| #2016 | [#2032](https://github.com/duck8823/traceary/pull/2032) | compact リマインダを 24h/ストア",
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "scratch path instruction",
			fact: "SCRATCH: use your private scratch dir /var/folders/cz/nrrglhxs5wn_pntxz0qzq1_80000gn/T/example",
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "ephemeral json envelope",
			fact: `{"ephemeralMessage": "starting the next wave now"}`,
			want: []string{extractionNoiseTransientStatus},
		},
		{
			name: "durable merge constraint stays visible",
			fact: "Never merge before Codex review",
			want: nil,
		},
		{
			name: "durable draft-pr policy stays visible",
			fact: "Always open Draft PRs first",
			want: nil,
		},
		{
			name: "durable worktree lesson stays visible",
			fact: "Use worktrees for parallel tickets",
			want: nil,
		},
		{
			name: "durable issue-closing changelog rule stays visible",
			fact: "Changelog entries must name the issue they close",
			want: nil,
		},
		{
			name: "durable draft-pr workflow without always/never stays visible",
			fact: "Open a Draft PR per issue before coding",
			want: nil,
		},
		{
			name: "durable japanese draft-pr rule stays visible",
			fact: "Draft PR は Codex review 後に ready にする",
			want: nil,
		},
		{
			name: "durable worktree lesson with two issue refs stays visible",
			fact: "Bundling #2010 and #2013 into one worktree broke the sub-issue wiring",
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := classifyExtractionNoise(tc.fact)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Fatalf("classifyExtractionNoise(%q) mismatch (-want +got):\n%s", tc.fact, diff)
			}
		})
	}
}
