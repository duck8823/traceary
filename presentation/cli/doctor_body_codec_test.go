package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/application"
)

type bodyCodecCheckerStub struct {
	state application.BodyCodecState
	err   error
}

func (s bodyCodecCheckerStub) CheckBodyCodec(context.Context) (application.BodyCodecState, error) {
	return s.state, s.err
}

func TestInspectBodyCodecPassesOnCleanStore(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	check := (&RootCLI{bodyCodecChecker: bodyCodecCheckerStub{}}).inspectBodyCodec(context.Background())
	if diff := cmp.Diff(doctorStatusPass, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	want := "all events use a supported body codec"
	if diff := cmp.Diff(want, check.Message); diff != "" {
		t.Fatalf("message mismatch (-want +got):\n%s", diff)
	}
}

func TestInspectBodyCodecWarnsOnSingleUnknownCodec(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	state := application.BodyCodecState{
		UnknownRows: []application.BodyCodecRow{
			{Codec: "zstd:19", Count: 1, SampleIDs: []string{"evt-abc"}},
		},
	}
	check := (&RootCLI{bodyCodecChecker: bodyCodecCheckerStub{state: state}}).inspectBodyCodec(context.Background())
	if diff := cmp.Diff(doctorStatusWarn, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	if check.Message == "" {
		t.Fatal("expected non-empty message")
	}
	if check.Hint == "" {
		t.Fatal("expected non-empty hint")
	}
}

func TestInspectBodyCodecWarnsOnMultipleUnknownCodecs(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	state := application.BodyCodecState{
		UnknownRows: []application.BodyCodecRow{
			{Codec: "gzip", Count: 3, SampleIDs: []string{"evt-1", "evt-2", "evt-3"}},
			{Codec: "zstd:19", Count: 2, SampleIDs: []string{"evt-4", "evt-5"}},
		},
	}
	check := (&RootCLI{bodyCodecChecker: bodyCodecCheckerStub{state: state}}).inspectBodyCodec(context.Background())
	if diff := cmp.Diff(doctorStatusWarn, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
	for _, codec := range []string{"gzip", "zstd:19"} {
		if !strings.Contains(check.Message, codec) {
			t.Fatalf("message %q missing codec %q", check.Message, codec)
		}
	}
}

func TestInspectBodyCodecSkipsWhenCheckerNil(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	check := (&RootCLI{}).inspectBodyCodec(context.Background())
	if diff := cmp.Diff(doctorStatusSkip, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
}

func TestInspectBodyCodecWarnsOnError(t *testing.T) {
	t.Setenv(cliLanguageEnvKey, "en")
	check := (&RootCLI{bodyCodecChecker: bodyCodecCheckerStub{err: errors.New("db error")}}).inspectBodyCodec(context.Background())
	if diff := cmp.Diff(doctorStatusWarn, check.Status); diff != "" {
		t.Fatalf("status mismatch (-want +got):\n%s", diff)
	}
}
