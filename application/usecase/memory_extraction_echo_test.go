package usecase

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestClassifyExtractionNoise_HidesInstructionEchoFragmentAndPayload(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		fact string
		want []string
	}{
		// Sanitized dogfood samples (2026-08-17/18): seven visible extracted
		// candidates that should have been extracted-hidden (#2112).
		{
			name: "numbered agent-rule always",
			fact: "1. Always run gofmt before opening a PR",
			want: []string{extractionNoiseInstructionEcho},
		},
		{
			name: "numbered agent-rule do not",
			fact: "2. Do not commit generated snapshots by hand",
			want: []string{extractionNoiseInstructionEcho},
		},
		{
			name: "bulleted agent-rule use",
			fact: "- Use the official brew binary against the live store",
			want: []string{extractionNoiseInstructionEcho},
		},
		{
			name: "japanese numbered request",
			fact: "3. レビュー前にテストを実行してください",
			want: []string{extractionNoiseInstructionEcho},
		},
		{
			name: "mid-sentence which continuation",
			fact: "which then requeues transient dead letters on the next hook",
			want: []string{extractionNoiseMidSentenceFragment},
		},
		{
			name: "mid-sentence and continuation",
			fact: "and the replica size can exceed the destination estimate",
			want: []string{extractionNoiseMidSentenceFragment},
		},
		{
			name: "payload echo ephemeral envelope",
			fact: `{"ephemeralMessage":"starting the next wave now"}`,
			want: []string{extractionNoiseTransientStatus, extractionNoisePayloadEcho},
		},
		{
			name: "payload echo remember-intent body",
			fact: `{"ephemeralMessage":{"text":"remember that we ship the keep list as 41 invocables"}}`,
			want: []string{extractionNoiseTransientStatus, extractionNoisePayloadEcho},
		},
		{
			name: "payload echo array literal",
			fact: `[{"path":"/tmp/scratch","kind":"note"}]`,
			want: []string{extractionNoisePayloadEcho},
		},
		// Counter-examples: facts and declarative lists stay visible.
		{name: "short durable fact", fact: "The two-pillar target is 41 invocables", want: nil},
		{name: "declarative numbered fact", fact: "1. Search projection generation is complete", want: nil},
		{name: "declarative bullet fact", fact: "- Freelist pages are reclaimable via store compact", want: nil},
		{
			name: "markdown heading is structural non-prose",
			fact: "## Durable memory commands",
			want: []string{extractionNoiseStructuralNonProse},
		},
		{
			name: "hunk header with trailing context is fragment and structural",
			fact: "@@ -135,85 +135,85 @@ func inspect()",
			want: []string{extractionNoiseDiffFragment, extractionNoiseStructuralNonProse},
		},
		{name: "lowercase complete claim", fact: "the store stays local-first after compact", want: nil},
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

func TestHidesDespiteRememberIntent_OnlyPayloadEcho(t *testing.T) {
	t.Parallel()
	if hidesDespiteRememberIntent([]string{extractionNoiseInstructionEcho}) {
		t.Fatal("instruction_echo must stay remember-intent visible")
	}
	if !hidesDespiteRememberIntent([]string{extractionNoiseTransientStatus, extractionNoisePayloadEcho}) {
		t.Fatal("payload_echo must hide even with remember-intent")
	}
}
