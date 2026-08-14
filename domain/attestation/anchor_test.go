package attestation_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/duck8823/traceary/domain/attestation"
)

func TestAnchorLineRoundTrip(t *testing.T) {
	t.Parallel()

	record := attestation.AnchorRecord{
		Version:     attestation.AnchorFormatVersion,
		Seq:         2,
		Head:        attestation.GenesisHex(),
		PublishedAt: "2026-08-14T17:00:00Z",
	}
	line, err := attestation.FormatAnchorLine(record)
	if err != nil {
		t.Fatalf("FormatAnchorLine() error = %v", err)
	}
	if bytes.Contains(line, []byte("\n")) {
		t.Fatalf("line contained a newline: %q", line)
	}
	got, err := attestation.ParseAnchorLine(line)
	if err != nil {
		t.Fatalf("ParseAnchorLine() error = %v", err)
	}
	if diff := cmp.Diff(record, got); diff != "" {
		t.Fatalf("record mismatch (-want +got):\n%s", diff)
	}
}

func TestParseAnchorFile_RejectsBackwardSeqAndConflictingHeads(t *testing.T) {
	t.Parallel()

	head := attestation.GenesisHex()
	other := strings.Repeat("ab", 32)
	ok := []attestation.AnchorRecord{
		{Version: 1, Seq: 0, Head: head, PublishedAt: "t0"},
		{Version: 1, Seq: 2, Head: other, PublishedAt: "t1"},
	}
	if err := attestation.CheckAnchorHistory(ok); err != nil {
		t.Fatalf("CheckAnchorHistory(gaps ok) error = %v", err)
	}

	backward := []attestation.AnchorRecord{
		{Version: 1, Seq: 2, Head: other, PublishedAt: "t0"},
		{Version: 1, Seq: 1, Head: head, PublishedAt: "t1"},
	}
	if err := attestation.CheckAnchorHistory(backward); err == nil {
		t.Fatal("CheckAnchorHistory(backward) error = nil")
	}

	conflict := []attestation.AnchorRecord{
		{Version: 1, Seq: 1, Head: head, PublishedAt: "t0"},
		{Version: 1, Seq: 1, Head: other, PublishedAt: "t1"},
	}
	if err := attestation.CheckAnchorHistory(conflict); err == nil {
		t.Fatal("CheckAnchorHistory(conflict) error = nil")
	}
}

func TestRelateAnchor(t *testing.T) {
	t.Parallel()

	head := attestation.GenesisHex()
	other := strings.Repeat("cd", 32)
	tests := []struct {
		name    string
		seq     int64
		store   string
		last    attestation.AnchorRecord
		present bool
		want    attestation.AnchorRelation
	}{
		{name: "missing", seq: 0, store: head, present: false, want: attestation.AnchorMissing},
		{name: "matches", seq: 1, store: head, last: attestation.AnchorRecord{Seq: 1, Head: head}, present: true, want: attestation.AnchorMatches},
		{name: "behind", seq: 2, store: other, last: attestation.AnchorRecord{Seq: 1, Head: head}, present: true, want: attestation.AnchorBehind},
		{name: "mismatch", seq: 1, store: other, last: attestation.AnchorRecord{Seq: 1, Head: head}, present: true, want: attestation.AnchorMismatch},
		{name: "ahead", seq: 1, store: head, last: attestation.AnchorRecord{Seq: 3, Head: other}, present: true, want: attestation.AnchorAhead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := attestation.RelateAnchor(tt.seq, tt.store, tt.last, tt.present)
			if got != tt.want {
				t.Fatalf("RelateAnchor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecideAnchorAppend(t *testing.T) {
	t.Parallel()

	head := attestation.GenesisHex()
	other := strings.Repeat("cd", 32)
	next := attestation.AnchorRecord{
		Version:     1,
		Seq:         2,
		Head:        other,
		PublishedAt: "t1",
	}
	last := attestation.AnchorRecord{Seq: 1, Head: head, PublishedAt: "t0"}

	appendLine, err := attestation.DecideAnchorAppend(last, false, next)
	if err != nil || !appendLine {
		t.Fatalf("missing file DecideAnchorAppend() = %v, %v, want append", appendLine, err)
	}

	same := next
	same.Seq = 1
	same.Head = head
	appendLine, err = attestation.DecideAnchorAppend(last, true, same)
	if err != nil || appendLine {
		t.Fatalf("same head DecideAnchorAppend() = %v, %v, want no-op", appendLine, err)
	}

	conflict := same
	conflict.Head = other
	if _, err := attestation.DecideAnchorAppend(last, true, conflict); err == nil {
		t.Fatal("conflicting head DecideAnchorAppend() error = nil")
	}

	behind := next
	behind.Seq = 0
	if _, err := attestation.DecideAnchorAppend(last, true, behind); err == nil {
		t.Fatal("ahead file DecideAnchorAppend() error = nil")
	}

	appendLine, err = attestation.DecideAnchorAppend(last, true, next)
	if err != nil || !appendLine {
		t.Fatalf("behind file DecideAnchorAppend() = %v, %v, want append", appendLine, err)
	}
}
