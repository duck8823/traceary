package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunBodyLocalityRefusesLiveStore(t *testing.T) {
	t.Parallel()
	live, err := defaultLiveStorePath()
	if err != nil {
		t.Fatalf("defaultLiveStorePath() error = %v", err)
	}
	_, err = runBodyLocality(context.Background(), bodyLocalityOpts{
		Dir:       filepath.Dir(live),
		Rows:      1,
		BodyBytes: 16,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("runBodyLocality(live dir) error = %v, want refusal", err)
	}
	_, err = runBodyLocality(context.Background(), bodyLocalityOpts{
		Dir:       live,
		Rows:      1,
		BodyBytes: 16,
	})
	if err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("runBodyLocality(live path) error = %v, want refusal", err)
	}
}

func TestRunBodyLocalityMeasuresInlineAndSideTable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "locality")
	report, err := runBodyLocality(context.Background(), bodyLocalityOpts{
		Dir:       dir,
		Rows:      48,
		BodyBytes: 4096,
		Seed:      bodyLocalitySeed,
		Iters:     3,
	})
	if err != nil {
		t.Fatalf("runBodyLocality() error = %v", err)
	}
	if report.SchemaVersion != bodyLocalitySchemaVersion {
		t.Fatalf("schema = %q", report.SchemaVersion)
	}
	if report.LiveStoreQueried {
		t.Fatal("scratch measurement must not query the live store")
	}
	if report.Decision != "material" && report.Decision != "not_material" {
		t.Fatalf("decision = %q", report.Decision)
	}
	if len(report.Corpora) != 2 {
		t.Fatalf("corpora = %d, want 2", len(report.Corpora))
	}
	for _, corpus := range report.Corpora {
		if len(corpus.Layouts) != 2 {
			t.Fatalf("%s layouts = %d", corpus.Kind, len(corpus.Layouts))
		}
		recomputed := computeLocalityGate(corpus.Layouts[0], corpus.Layouts[1])
		if recomputed != corpus.Gate {
			t.Fatalf("%s gate not derived from timings: got %+v want %+v", corpus.Kind, corpus.Gate, recomputed)
		}
		var inlineEvents, sideEvents int64
		for _, layout := range corpus.Layouts {
			if layout.DatabaseBytes <= 0 {
				t.Fatalf("%s %s missing database bytes", corpus.Kind, layout.Name)
			}
			foundMeta := false
			for _, item := range layout.Cases {
				if len(item.QueryPlan) == 0 {
					t.Fatalf("%s %s %s has no query plan", corpus.Kind, layout.Name, item.Name)
				}
				if item.Name == "events_meta_5000" {
					foundMeta = true
					if layout.Name == "inline" {
						inlineEvents = layout.EventsTableBytes
					} else {
						sideEvents = layout.EventsTableBytes
					}
				}
			}
			if !foundMeta {
				t.Fatalf("%s %s missing events_meta_5000", corpus.Kind, layout.Name)
			}
		}
		if inlineEvents > 0 && sideEvents > 0 && sideEvents >= inlineEvents {
			t.Fatalf("%s side-table events pages %d did not drop below inline %d", corpus.Kind, sideEvents, inlineEvents)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "body-locality.json"))
	if err != nil {
		t.Fatalf("read body-locality.json: %v", err)
	}
	var onDisk bodyLocalityReport
	if err := json.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("body-locality.json: %v", err)
	}
	if onDisk.Decision != report.Decision {
		t.Fatalf("disk decision %q != %q", onDisk.Decision, report.Decision)
	}
}

func TestDecideBodyLocalityUsesGates(t *testing.T) {
	t.Parallel()
	decision, reason := decideBodyLocality([]bodyLocalityCorpus{
		{Kind: "entropy", Gate: bodyLocalityGate{MethodValid: true, Material: true, InlineOverProjectionWarm: 3, InlineOverSideWarm: 3, SideOverProjectionWarm: 1}},
		{Kind: "repetitive", Gate: bodyLocalityGate{MethodValid: false, Material: false, InlineOverProjectionWarm: 1.1, InlineOverSideWarm: 1.05, SideOverProjectionWarm: 1.05}},
	})
	if decision != "not_material" || !strings.Contains(reason, "compressed") {
		t.Fatalf("decision=%q reason=%q", decision, reason)
	}
}
