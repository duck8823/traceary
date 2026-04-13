package usecase

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	domtypes "github.com/duck8823/traceary/domain/types"
)

func TestInferArtifactRefs_BroadensSupportedFilePatterns(t *testing.T) {
	t.Parallel()

	refs, err := inferArtifactRefs(
		"Artifact: src/app.tsx. scripts/check.py Formula/traceary.rb configs/release.yaml.tpl docs/release/README. bin/traceary https://github.com/duck8823/traceary/blob/main/scripts/install.sh 2026/04/13",
	)
	if err != nil {
		t.Fatalf("inferArtifactRefs() error = %v", err)
	}

	got := make([]string, 0, len(refs))
	for _, ref := range refs {
		got = append(got, ref.Kind().String()+":"+ref.Value())
	}

	want := []string{
		"url:https://github.com/duck8823/traceary/blob/main/scripts/install.sh",
		"file:src/app.tsx",
		"file:scripts/check.py",
		"file:Formula/traceary.rb",
		"file:configs/release.yaml.tpl",
		"file:docs/release/README",
		"file:bin/traceary",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("artifact refs mismatch (-want +got):\n%s", diff)
	}
}

func TestLooksPathLikeArtifact_RejectsDateLikePaths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "date like", value: "2026/04/13", want: false},
		{name: "url path", value: "https://example.com/path", want: false},
		{name: "slash prose", value: "pros/cons", want: false},
		{name: "slash prose short", value: "and/or", want: false},
		{name: "source path", value: "src/app.tsx", want: true},
		{name: "extensionless path", value: "bin/traceary", want: true},
		{name: "extensionless docs path", value: "docs/release/README", want: true},
		{name: "relative path", value: "./tmp/output", want: true},
		{name: "version prose", value: "v1.2/v1.3", want: false},
		{name: "generic source path", value: "foo/bar.go", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksPathLikeArtifact(tc.value); got != tc.want {
				t.Fatalf("looksPathLikeArtifact(%q) = %v, want %v", tc.value, got, tc.want)
			}
		})
	}
}

func TestInferArtifactRefs_DoesNotTreatSlashSeparatedProseAsFiles(t *testing.T) {
	t.Parallel()

	refs, err := inferArtifactRefs("Refer to the pros/cons discussion and compare v1.2/v1.3, but keep docs/release/README, bin/traceary, and foo/bar.go in sync.")
	if err != nil {
		t.Fatalf("inferArtifactRefs() error = %v", err)
	}

	got := make([]string, 0, len(refs))
	for _, ref := range refs {
		got = append(got, ref.Kind().String()+":"+ref.Value())
	}

	want := []string{
		"file:docs/release/README",
		"file:bin/traceary",
		"file:foo/bar.go",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("artifact refs mismatch (-want +got):\n%s", diff)
	}
}

func TestInferArtifactRefs_ProducesValidArtifactRefs(t *testing.T) {
	t.Parallel()

	refs, err := inferArtifactRefs("Artifact: scripts/check.py and docs/release/README")
	if err != nil {
		t.Fatalf("inferArtifactRefs() error = %v", err)
	}

	for _, ref := range refs {
		if _, err := domtypes.ArtifactRefOf(ref.Kind(), ref.Value()); err != nil {
			t.Fatalf("ArtifactRefOf(%s, %q) error = %v", ref.Kind(), ref.Value(), err)
		}
	}
}
