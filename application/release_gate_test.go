package application_test

import (
	"testing"

	"github.com/duck8823/traceary/application"
)

func TestClassifyUpperBound(t *testing.T) {
	t.Parallel()
	if got := application.ClassifyUpperBound(false, 9, 1); got != application.ReleaseGateStatusSkip {
		t.Fatalf("unmeasured = %q", got)
	}
	if got := application.ClassifyUpperBound(true, 2.0, 2.0); got != application.ReleaseGateStatusPass {
		t.Fatalf("on bound = %q", got)
	}
	if got := application.ClassifyUpperBound(true, 2.01, 2.0); got != application.ReleaseGateStatusMiss {
		t.Fatalf("over bound = %q", got)
	}
	if got := application.ClassifyStrictUpperBound(true, 0.05, 0.05); got != application.ReleaseGateStatusMiss {
		t.Fatalf("on strict bound = %q", got)
	}
	if got := application.ClassifyStrictUpperBound(true, 0.049, 0.05); got != application.ReleaseGateStatusPass {
		t.Fatalf("under strict bound = %q", got)
	}
}

func TestClassifyLowerBound(t *testing.T) {
	t.Parallel()
	if got := application.ClassifyLowerBound(true, 0.95, 0.95); got != application.ReleaseGateStatusPass {
		t.Fatalf("on bound = %q", got)
	}
	if got := application.ClassifyLowerBound(true, 0.94, 0.95); got != application.ReleaseGateStatusMiss {
		t.Fatalf("under bound = %q", got)
	}
}

func TestRefuseLiveStore(t *testing.T) {
	t.Parallel()
	live, err := application.DefaultLiveStorePath()
	if err != nil {
		t.Fatalf("DefaultLiveStorePath() error = %v", err)
	}
	if err := application.RefuseLiveStore(live); err == nil {
		t.Fatal("RefuseLiveStore(live) = nil, want error")
	}
	if err := application.RefuseLiveStore(t.TempDir() + "/fixture.db"); err != nil {
		t.Fatalf("RefuseLiveStore(fixture) = %v", err)
	}
}
