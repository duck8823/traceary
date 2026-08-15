package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/duck8823/traceary/application"
)

func TestRunEvaluateGatesRefusesLiveStore(t *testing.T) {
	t.Parallel()
	live, err := application.DefaultLiveStorePath()
	if err != nil {
		t.Fatalf("DefaultLiveStorePath() error = %v", err)
	}
	var out bytes.Buffer
	err = runEvaluateGates(context.Background(), &out, live)
	if err == nil || !strings.Contains(err.Error(), "refusing the default live store") {
		t.Fatalf("runEvaluateGates(live) error = %v, want refusal", err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout = %q, want empty on live-store refusal", out.String())
	}
}
