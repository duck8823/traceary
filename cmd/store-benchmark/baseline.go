package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

type baselineArtifact struct {
	SchemaVersion string `json:"schema_version"`
	Capacity      struct {
		SchemaVersion string `json:"schema_version"`
		DatabaseBytes int64  `json:"database_bytes"`
		FreePages     *int64 `json:"free_pages"`
		WALBytes      *int64 `json:"wal_bytes"`
		Evidence      struct {
			Status string `json:"status"`
		} `json:"evidence"`
	} `json:"capacity"`
	Benchmark report `json:"benchmark"`
}

func validateBaselineFile(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read baseline artifact: %w", err)
	}
	var artifact baselineArtifact
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&artifact); err != nil {
		return fmt.Errorf("decode baseline artifact: %w", err)
	}
	return validateBaseline(artifact)
}

func validateBaseline(artifact baselineArtifact) error {
	if artifact.SchemaVersion != "traceary.capacity-baseline/v1" || artifact.Capacity.SchemaVersion != "traceary.capacity/v1" || artifact.Benchmark.SchemaVersion != "traceary.store-benchmark/v1" {
		return fmt.Errorf("baseline schema versions are incomplete")
	}
	if artifact.Benchmark.Status != "passed" && artifact.Benchmark.Status != "timeout" {
		return fmt.Errorf("benchmark status must be passed or timeout")
	}
	const target int64 = 22_978_910_618
	const tolerance int64 = 256 << 20
	if artifact.Capacity.DatabaseBytes < target-tolerance || artifact.Capacity.DatabaseBytes > target+tolerance {
		return fmt.Errorf("database_bytes is outside the sanitized 21.4 GiB shape")
	}
	if artifact.Capacity.FreePages == nil || artifact.Capacity.WALBytes == nil || *artifact.Capacity.FreePages < 0 || *artifact.Capacity.WALBytes < 0 {
		return fmt.Errorf("capacity free_pages and wal_bytes are required and must be non-negative")
	}
	if artifact.Capacity.Evidence.Status != "complete" && artifact.Capacity.Evidence.Status != "unavailable" {
		return fmt.Errorf("capacity evidence status must be explicit")
	}
	wanted := map[string]bool{"active": false, "latest": false, "handoff": false, "search": false}
	hasTimeout := false
	for _, item := range artifact.Benchmark.Cases {
		if _, ok := wanted[item.Name]; !ok {
			return fmt.Errorf("unexpected benchmark case %q", item.Name)
		}
		if len(item.QueryPlan) == 0 {
			return fmt.Errorf("benchmark case %q has no query plan", item.Name)
		}
		switch item.Status {
		case "timeout":
			hasTimeout = true
			if item.TimeoutMS <= 0 || item.ElapsedLowerBoundUS <= 0 {
				return fmt.Errorf("benchmark case %q timeout has no positive limit", item.Name)
			}
		case "passed":
			if item.Name == "search" && (item.MatchedRows == nil || *item.MatchedRows < 0) {
				return fmt.Errorf("passed search case requires non-negative matched_rows")
			}
			if item.ColdP50US <= 0 || item.ColdP95US <= 0 || item.WarmP50US <= 0 || item.WarmP95US <= 0 {
				return fmt.Errorf("benchmark case %q has placeholder timing", item.Name)
			}
		default:
			return fmt.Errorf("benchmark case %q has invalid status", item.Name)
		}
		wanted[item.Name] = true
	}
	if hasTimeout != (artifact.Benchmark.Status == "timeout") {
		return fmt.Errorf("benchmark report status does not match case statuses")
	}
	for name, found := range wanted {
		if !found {
			return fmt.Errorf("benchmark case %q is missing", name)
		}
	}
	return nil
}
