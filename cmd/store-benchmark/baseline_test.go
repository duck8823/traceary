package main

import "testing"

func TestValidateBaselineRequiresMeasuredFourCaseEvidence(t *testing.T) {
	var artifact baselineArtifact
	artifact.SchemaVersion = "traceary.capacity-baseline/v1"
	artifact.Capacity.SchemaVersion = "traceary.capacity/v1"
	artifact.Capacity.DatabaseBytes = 22_978_910_618
	metric := int64(1)
	artifact.Capacity.FreePages = &metric
	artifact.Capacity.WALBytes = &metric
	artifact.Capacity.Evidence.Status = "complete"
	artifact.Benchmark.SchemaVersion = "traceary.store-benchmark/v1"
	artifact.Benchmark.Status = "passed"
	for _, name := range []string{"active", "latest", "handoff", "search"} {
		artifact.Benchmark.Cases = append(artifact.Benchmark.Cases, caseResult{Name: name, Status: "passed", ColdP50US: 1, ColdP95US: 2, WarmP50US: 1, WarmP95US: 2, QueryPlan: []string{"SCAN production_index"}})
	}
	if err := validateBaseline(artifact); err != nil {
		t.Fatalf("validateBaseline() error = %v", err)
	}
	artifact.Benchmark.Cases[0].ColdP50US = 0
	if err := validateBaseline(artifact); err == nil {
		t.Fatal("placeholder timing accepted")
	}
	artifact.Benchmark.Status = "timeout"
	artifact.Benchmark.Cases[0] = caseResult{Name: "active", Status: "timeout", TimeoutMS: 120000, ElapsedLowerBoundUS: 120000000, QueryPlan: []string{"SCAN production_index"}}
	artifact.Benchmark.Cases[1].ColdP50US = 1
	if err := validateBaseline(artifact); err != nil {
		t.Fatalf("timeout baseline rejected: %v", err)
	}
}
