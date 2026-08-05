package domain

import (
	"bytes"
	"testing"
	"time"
)

func summaryTestUnit(t *testing.T, sequence uint64, workspace, session, kind string, audit bool) HistoryUnit {
	t.Helper()
	values := make([]SQLiteValue, len(ArchiveEventV1Columns()))
	for i := range values {
		values[i] = NullValue()
	}
	values[0] = TextValue([]byte("event" + string(rune(sequence))))
	values[1] = TextValue([]byte(kind))
	values[2] = TextValue([]byte("agent"))
	values[3] = TextValue([]byte(session))
	values[5] = TextValue([]byte(time.Unix(int64(sequence), 0).UTC().Format(time.RFC3339Nano)))
	values[6] = TextValue([]byte("client"))
	values[7] = TextValue([]byte(workspace))
	event, err := NewArchiveEventV1(values)
	if err != nil {
		t.Fatal(err)
	}
	unit := HistoryUnit{Sequence: sequence, Event: event}
	if audit {
		auditValues := make([]SQLiteValue, len(ArchiveAuditV1Columns()))
		for i := range auditValues {
			auditValues[i] = NullValue()
		}
		row, buildErr := NewArchiveAuditV1(auditValues)
		if buildErr != nil {
			t.Fatal(buildErr)
		}
		unit.Audit = &row
	}
	return unit
}

func summaryTestConfig() SegmentSummaryGeneratorConfig {
	return SegmentSummaryGeneratorConfig{FilterKeyID: "filter-v1", HMACVersion: SegmentSummaryHMACV1, MaxUnits: 100, MaxDistinctPerKind: 2, MaxSessions: 10, BloomBitCount: 1024, BloomHashCount: 5, BloomMaxSetPermille: 900}
}

func TestGenerateSegmentSummaryIsDeterministicAndHasNoFalseNegatives(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	units := []HistoryUnit{summaryTestUnit(t, 1, "alpha", "s1", "note", true), summaryTestUnit(t, 2, "β/作業場", "s1", "command", false), summaryTestUnit(t, 3, "gamma", "s2", "note", true)}
	first, err := GenerateSegmentCatalogSummaryV1(units, key, summaryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	second, err := GenerateSegmentCatalogSummaryV1(units, key, summaryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := first.CanonicalBytes(1 << 20)
	b, _ := second.CanonicalBytes(1 << 20)
	if !bytes.Equal(a, b) {
		t.Fatal("generation is not deterministic")
	}
	for _, workspace := range []string{"alpha", "β/作業場", "gamma"} {
		if !SegmentSummaryMayMatch(first, "filter-v1", key, SegmentSummaryPredicate{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte(workspace)}) {
			t.Fatalf("false negative for %q", workspace)
		}
	}
	workspaceExact := 0
	for _, token := range first.ExactTokens {
		if token.Kind == SummaryTokenWorkspace {
			workspaceExact++
		}
	}
	if workspaceExact != 0 {
		t.Fatalf("exact cap transition retained partial set: %d", workspaceExact)
	}
	if len(first.Sessions) != 2 {
		t.Fatalf("sessions = %d", len(first.Sessions))
	}
}

func TestSegmentSummaryUnknownCasesSelectAndBloomMissExcludes(t *testing.T) {
	key := bytes.Repeat([]byte{3}, 32)
	summary, err := GenerateSegmentCatalogSummaryV1([]HistoryUnit{summaryTestUnit(t, 1, "alpha", "session", "note", false)}, key, summaryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	cases := []SegmentSummaryPredicate{
		{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateNotEqual, Value: []byte("alpha")},
		{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateContains, Value: []byte("alpha")},
		{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte("a")},
		{Kind: 99, Operator: SegmentSummaryPredicateEqual, Value: []byte("alpha")},
	}
	for _, predicate := range cases {
		if !SegmentSummaryMayMatch(summary, "filter-v1", key, predicate) {
			t.Fatal("unknown predicate excluded segment")
		}
	}
	if !SegmentSummaryMayMatch(summary, "other-key", key, SegmentSummaryPredicate{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte("absent")}) {
		t.Fatal("key mismatch excluded segment")
	}
	if SegmentSummaryMayMatch(summary, "filter-v1", key, SegmentSummaryPredicate{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte("definitely-absent")}) {
		t.Fatal("complete Bloom miss did not exclude")
	}
}

func TestBloomSaturationDegradesToUnknown(t *testing.T) {
	key := bytes.Repeat([]byte{9}, 32)
	cfg := summaryTestConfig()
	cfg.BloomBitCount = 8
	cfg.BloomHashCount = 8
	cfg.BloomMaxSetPermille = 100
	cfg.MaxDistinctPerKind = 1
	summary, err := GenerateSegmentCatalogSummaryV1([]HistoryUnit{summaryTestUnit(t, 1, "alpha", "session", "note", false), summaryTestUnit(t, 2, "beta", "session", "note", false)}, key, cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, bloom := range summary.Blooms {
		if bloom.Kind == SummaryTokenWorkspace {
			t.Fatal("saturated Bloom was published")
		}
	}
	if !SegmentSummaryMayMatch(summary, "filter-v1", key, SegmentSummaryPredicate{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte("absent")}) {
		t.Fatal("saturation did not degrade to unknown")
	}
}

func TestNoncanonicalBloomAlwaysDegradesToUnknown(t *testing.T) {
	key := bytes.Repeat([]byte{4}, 32)
	predicate := SegmentSummaryPredicate{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateEqual, Value: []byte("absent")}
	for _, summary := range []SegmentCatalogSummaryV1{
		{FilterKeyID: "filter-v1", Blooms: []SegmentBloomV1{{Kind: SummaryTokenWorkspace, BitCount: 8, HashCount: 17, Bits: []byte{0}}}},
		{FilterKeyID: "filter-v1", Blooms: []SegmentBloomV1{{Kind: SummaryTokenWorkspace, BitCount: 8, HashCount: 1, Bits: []byte{0}}, {Kind: SummaryTokenWorkspace, BitCount: 8, HashCount: 1, Bits: []byte{0}}}},
		{FilterKeyID: "filter-v1", Blooms: []SegmentBloomV1{{Kind: SummaryTokenWorkspace, BitCount: 16, HashCount: 1, Bits: []byte{0}}}},
		{FilterKeyID: "filter-v1", Blooms: []SegmentBloomV1{{Kind: SummaryTokenWorkspace, BitCount: SegmentSummaryBloomMaxBitsV1 + 8, HashCount: 1, Bits: make([]byte, (SegmentSummaryBloomMaxBitsV1+8)/8)}}},
	} {
		if !SegmentSummaryMayMatch(summary, "filter-v1", key, predicate) {
			t.Fatal("noncanonical Bloom produced negative evidence")
		}
	}
}

func TestConservativeCandidatePlannerKeepsUnknownOnlyInsideTimeEnvelope(t *testing.T) {
	key := bytes.Repeat([]byte{5}, 32)
	summary, err := GenerateSegmentCatalogSummaryV1([]HistoryUnit{summaryTestUnit(t, 1, "alpha", "session", "note", false)}, key, summaryTestConfig())
	if err != nil {
		t.Fatal(err)
	}
	segmentMin, segmentMax := time.Unix(10, 0), time.Unix(20, 0)
	negative := []SegmentSummaryPredicate{{Kind: SummaryTokenWorkspace, Operator: SegmentSummaryPredicateNotEqual, Value: []byte("alpha")}}
	if !SegmentCatalogCandidateMayMatch(summary, segmentMin, segmentMax, time.Unix(15, 0), time.Unix(16, 0), "filter-v1", key, negative) {
		t.Fatal("unknown predicate excluded an overlapping segment")
	}
	if SegmentCatalogCandidateMayMatch(summary, segmentMin, segmentMax, time.Unix(30, 0), time.Unix(40, 0), "filter-v1", key, negative) {
		t.Fatal("disjoint complete time envelope matched")
	}
	summary.TimeComplete = false
	if !SegmentCatalogCandidateMayMatch(summary, segmentMin, segmentMax, time.Unix(30, 0), time.Unix(40, 0), "filter-v1", key, negative) {
		t.Fatal("incomplete time evidence excluded a segment")
	}
	summary.TimeComplete = true
	if !SegmentCatalogCandidateMayMatch(summary, time.Unix(20, 0), time.Unix(10, 0), time.Unix(15, 0), time.Unix(16, 0), "filter-v1", key, nil) {
		t.Fatal("reversed segment bounds excluded a segment")
	}
}
