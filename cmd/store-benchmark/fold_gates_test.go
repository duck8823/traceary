package main

import (
	"context"
	"strings"
	"testing"
)

func TestRunFoldGatesRefusesLiveStore(t *testing.T) {
	t.Parallel()
	live, err := defaultLiveStorePath()
	if err != nil {
		t.Fatalf("defaultLiveStorePath() error = %v", err)
	}
	err = runFoldGates(context.Background(), live, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "refusing the default live store") {
		t.Fatalf("runFoldGates(live) error = %v, want refusal", err)
	}
}
