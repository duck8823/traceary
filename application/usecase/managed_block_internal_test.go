package usecase

import (
	"regexp"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	apptypes "github.com/duck8823/traceary/application/types"
)

// fakeMarkers exercises managedBlockMarkers with a marker prefix that is
// not the v0.12 Codex `traceary-memories` marker. The host-context import
// stub planned for v0.13.0-3 reuses the same parser with different
// markers, so this confirms the primitive is host-agnostic and not
// accidentally coupled to memoryBridgeBlockMarkers.
var fakeMarkers = managedBlockMarkers{
	Begin:          "<!-- fake:begin:v1 -->",
	End:            "<!-- fake:end -->",
	BeginPattern:   regexp.MustCompile(`^<!-- fake:begin:v(\d+) -->$`),
	CurrentVersion: 1,
}

func TestManagedBlockMarkers_FindRegion_ReturnsRegionWithCustomMarkers(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		"prelude",
		fakeMarkers.Begin,
		"managed body",
		fakeMarkers.End,
		"trailer",
		"",
	}, "\n")
	region, found, err := fakeMarkers.findRegion(content)
	if err != nil {
		t.Fatalf("findRegion error = %v", err)
	}
	if !found {
		t.Fatalf("findRegion found = false, want true")
	}
	if got := content[region.start:region.end]; got != fakeMarkers.Begin+"\nmanaged body\n"+fakeMarkers.End+"\n" {
		t.Fatalf("region content mismatch: %q", got)
	}
}

func TestManagedBlockMarkers_FindRegion_RejectsDuplicateBegin(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		fakeMarkers.Begin,
		"first",
		fakeMarkers.End,
		"",
		fakeMarkers.Begin,
		"second",
		fakeMarkers.End,
		"",
	}, "\n")
	_, _, err := fakeMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "multiple Traceary managed memory blocks") {
		t.Fatalf("findRegion error = %v, want duplicate-begin rejection", err)
	}
}

func TestManagedBlockMarkers_FindRegion_RejectsNewerVersion(t *testing.T) {
	t.Parallel()

	content := "<!-- fake:begin:v9 -->\nfuture\n" + fakeMarkers.End + "\n"
	_, _, err := fakeMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "refusing to overwrite newer Traceary managed block version v9") {
		t.Fatalf("findRegion error = %v, want newer-version rejection", err)
	}
}

func TestManagedBlockMarkers_FindRegion_RejectsOrphanBegin(t *testing.T) {
	t.Parallel()

	content := fakeMarkers.Begin + "\nbody without end\n"
	_, _, err := fakeMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "without end marker") {
		t.Fatalf("findRegion error = %v, want orphan-begin rejection", err)
	}
}

func TestManagedBlockMarkers_FindRegion_IgnoresOrphanEnd(t *testing.T) {
	t.Parallel()

	content := "user prose mentioning " + fakeMarkers.End + " literally\n"
	_, found, err := fakeMarkers.findRegion(content)
	if err != nil {
		t.Fatalf("findRegion error = %v", err)
	}
	if found {
		t.Fatalf("findRegion found = true, want false (orphan end is treated as user content)")
	}
}

func TestManagedBlockMarkers_FindRegion_RejectsDuplicateEnd(t *testing.T) {
	t.Parallel()

	content := strings.Join([]string{
		fakeMarkers.Begin,
		"body",
		fakeMarkers.End,
		fakeMarkers.End,
		"",
	}, "\n")
	_, _, err := fakeMarkers.findRegion(content)
	if err == nil || !strings.Contains(err.Error(), "multiple Traceary managed memory end markers") {
		t.Fatalf("findRegion error = %v, want duplicate-end rejection", err)
	}
}

func TestManagedBlockMarkers_ReplaceOrAppend_CreatesWhenAbsent(t *testing.T) {
	t.Parallel()

	managed := fakeMarkers.Begin + "\nfresh\n" + fakeMarkers.End + "\n"
	got, action, err := fakeMarkers.replaceOrAppend("", false, managed)
	if err != nil {
		t.Fatalf("replaceOrAppend error = %v", err)
	}
	if action != apptypes.MemoryActivationApplyCreated {
		t.Fatalf("action = %q, want created", action)
	}
	if got != managed {
		t.Fatalf("content = %q, want %q", got, managed)
	}
}

func TestManagedBlockMarkers_ReplaceOrAppend_ReplacesExistingRegion(t *testing.T) {
	t.Parallel()

	existing := "preface\n\n" + fakeMarkers.Begin + "\nstale\n" + fakeMarkers.End + "\n\nepilogue\n"
	managed := fakeMarkers.Begin + "\nrefreshed\n" + fakeMarkers.End + "\n"
	got, action, err := fakeMarkers.replaceOrAppend(existing, true, managed)
	if err != nil {
		t.Fatalf("replaceOrAppend error = %v", err)
	}
	if action != apptypes.MemoryActivationApplyUpdated {
		t.Fatalf("action = %q, want updated", action)
	}
	if !strings.Contains(got, "preface\n") || !strings.Contains(got, "epilogue\n") {
		t.Fatalf("user-authored content lost: %q", got)
	}
	if strings.Contains(got, "stale") {
		t.Fatalf("stale managed body retained: %q", got)
	}
	if !strings.Contains(got, "refreshed") {
		t.Fatalf("fresh managed body missing: %q", got)
	}
}

func TestManagedBlockMarkers_ReplaceOrAppend_NoopWhenIdentical(t *testing.T) {
	t.Parallel()

	managed := fakeMarkers.Begin + "\nstable\n" + fakeMarkers.End + "\n"
	existing := "user notes\n\n" + managed
	got, action, err := fakeMarkers.replaceOrAppend(existing, true, managed)
	if err != nil {
		t.Fatalf("replaceOrAppend error = %v", err)
	}
	if action != apptypes.MemoryActivationApplyNoop {
		t.Fatalf("action = %q, want noop", action)
	}
	if got != existing {
		t.Fatalf("content changed for identical block: %q", got)
	}
}

func TestManagedBlockMarkers_ReplaceOrAppend_AppendsWithSpacing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		existing string
		want     string
	}{
		{
			name:     "no trailing newline",
			existing: "intro line",
			want:     "intro line\n\nBLOCK",
		},
		{
			name:     "single trailing newline",
			existing: "intro line\n",
			want:     "intro line\n\nBLOCK",
		},
		{
			name:     "blank line already present",
			existing: "intro line\n\n",
			want:     "intro line\n\nBLOCK",
		},
		{
			name:     "empty",
			existing: "",
			want:     "BLOCK",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := appendManagedBlockWithSpacing(tc.existing, "BLOCK")
			if got != tc.want {
				t.Fatalf("appendManagedBlockWithSpacing(%q, BLOCK) = %q, want %q", tc.existing, got, tc.want)
			}
		})
	}
}

func TestMemoryBridgeBlockMarkers_FindsCanonicalCodexRegion(t *testing.T) {
	t.Parallel()

	content := "preface\n\n" + MemoryBridgeMarkerBegin + "\nbody\n" + MemoryBridgeMarkerEnd + "\n"
	region, found, err := memoryBridgeBlockMarkers.findRegion(content)
	if err != nil {
		t.Fatalf("findRegion error = %v", err)
	}
	if !found {
		t.Fatalf("found = false, want canonical Codex marker recognized")
	}
	got := content[region.start:region.end]
	want := MemoryBridgeMarkerBegin + "\nbody\n" + MemoryBridgeMarkerEnd + "\n"
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("canonical region mismatch (-want +got):\n%s", diff)
	}
}
