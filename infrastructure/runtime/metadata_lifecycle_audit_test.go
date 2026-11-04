//go:build darwin || linux

package runtime_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	infra "github.com/duck8823/traceary/infrastructure/runtime"
)

// These schema fixtures exercise audit behavior. Separate combined-source
// qualification must additionally use the pinned lifecycle writer and readers.
func metadataLifecycleFixture(t *testing.T) (*infra.MetadataStore, string, map[string]any) {
	t.Helper()
	m, execution, root := metadataAuditFixture(t)
	runs, err := execution.ListLaunches(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	r := runs[0]
	epoch := strings.Repeat("a", 32)
	if epoch == r.Identity.Epoch {
		epoch = strings.Repeat("b", 32)
	}
	selection := map[string]any{"epoch": r.Identity.Epoch, "run": r.Identity.Run, "generation": r.Identity.Generation, "worktree": string(r.Target.Worktree().ID()), "profile": string(r.Profile.ID())}
	doc := map[string]any{"version": 1, "revision": 7, "epoch": epoch, "clean": false, "previous_epoch": r.Identity.Epoch, "previous_clean": false, "interrupted": []any{map[string]any{"selection": selection, "process_unknown": true, "pending_controls": 1024}}, "shutdowns": []any{map[string]any{"binding": map[string]any{"epoch": r.Identity.Epoch, "preview_id": strings.Repeat("c", 32), "revision": "18446744073709551615", "roster_digest": strings.Repeat("d", 64), "total_runs": 1, "pending_launches": 0, "pending_controls": 0, "pending_reconciliations": 0}, "outcome": "unknown", "closed_terminals": 0}}, "update": map[string]any{"version": "v1.0+test", "protocol": 1, "started": "2026-11-01T00:00:00Z", "notified_24h": true}, "autostart_opt_in": true}
	view, err := m.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	witness := view.RootWitness
	writeMetadataFixture(t, root, "lifecycle.json", doc)
	writeMetadataFixture(t, root, "endpoints.json", map[string]any{"version": 1, "revision": 3, "epoch": epoch, "root_witness": witness, "endpoints": []any{map[string]any{"name": "daemon.sock", "role": "management", "uid": os.Geteuid(), "dev": "1", "ino": "2", "ctime_sec": 1, "ctime_nsec": 0}, map[string]any{"name": "observations.sock", "role": "observations", "uid": os.Geteuid(), "dev": "1", "ino": "3", "ctime_sec": 1, "ctime_nsec": 0}}})
	return m, root, doc
}
func writeMetadataFixture(t *testing.T, root, name string, doc any) {
	t.Helper()
	b, e := json.Marshal(doc)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(root, name), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func readMetadataFixture(t *testing.T, root, name string) map[string]any {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(root, name))
	if e != nil {
		t.Fatal(e)
	}
	var d map[string]any
	if e = json.Unmarshal(b, &d); e != nil {
		t.Fatal(e)
	}
	return d
}

func TestMetadataLifecycleAuditForgetRetainsPoliciesAndEndpointEvidence(t *testing.T) {
	m, root, doc := metadataLifecycleFixture(t)
	ctx := context.Background()
	endpoints, err := os.ReadFile(filepath.Join(root, "endpoints.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := m.PreviewForget(ctx)
	if err != nil {
		t.Fatal("supported lifecycle root refused", err)
	}
	encoded, _ := json.Marshal(p.Snapshot)
	var summary map[string]any
	_ = json.Unmarshal(encoded, &summary)
	if summary["interrupted_runs"] != float64(1) || summary["retained_shutdowns"] != float64(1) || summary["lifecycle_revision"] != "7" || summary["endpoint_revision"] != "3" {
		t.Fatal("missing lifecycle scope", string(encoded))
	}
	c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := m.Forget(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(result)
	var resultDoc map[string]any
	_ = json.Unmarshal(b, &resultDoc)
	if resultDoc["interrupted_runs"] != float64(1) {
		t.Fatal("missing erased interruption scope")
	}
	after := readMetadataFixture(t, root, "lifecycle.json")
	delete(after, "revision")
	delete(after, "interrupted")
	delete(doc, "revision")
	delete(doc, "interrupted")
	want, _ := json.Marshal(doc)
	got, _ := json.Marshal(after)
	if !bytes.Equal(want, got) {
		t.Fatal("retained lifecycle policy/audit changed")
	}
	current, _ := os.ReadFile(filepath.Join(root, "endpoints.json"))
	if !bytes.Equal(current, endpoints) {
		t.Fatal("endpoint evidence changed")
	}
	if _, err = m.Export(ctx); err != nil {
		t.Fatal("forgotten prefix no longer inspectable", err)
	}
}

func TestMetadataLifecycleForgetEveryFailurePrefixAndFreshRecovery(t *testing.T) {
	for at := 1; at <= 3; at++ {
		for _, after := range []bool{false, true} {
			t.Run(string(rune('0'+at))+map[bool]string{false: "-before", true: "-after"}[after], func(t *testing.T) {
				m, root, _ := metadataLifecycleFixture(t)
				ctx := context.Background()
				p, err := m.PreviewForget(ctx)
				if err != nil {
					t.Fatal(err)
				}
				c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
				infra.FailMetadataCommitForTest(m, at, after)
				r, err := m.Forget(ctx, c)
				if err == nil || r.Outcome != "" || errors.Is(err, app.ErrMetadataForgetUncertain) != (at > 1 || after) {
					t.Fatal("wrong failure admission", err)
				}
				fresh, err := infra.OpenMetadataStore(root, infra.MaxRegistryBytes)
				if err != nil {
					t.Fatal(err)
				}
				p, err = fresh.PreviewForget(ctx)
				if err != nil {
					t.Fatal("invalid persisted prefix", err)
				}
				c, err = domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = fresh.Forget(ctx, c); err != nil {
					t.Fatal("fresh recovery failed", err)
				}
			})
		}
	}
}

func TestMetadataLifecycleRejectsMalformedAndFutureSchemasWithoutEffects(t *testing.T) {
	for _, tc := range []struct {
		name, file string
		mutate     func(map[string]any)
		want       error
	}{
		{"future-lifecycle-new-null", "lifecycle.json", func(d map[string]any) { d["version"] = 2; d["new_field"] = nil }, domain.ErrUnsupported},
		{"future-endpoint-new-null", "endpoints.json", func(d map[string]any) { d["version"] = 2; d["new_field"] = nil }, domain.ErrUnsupported},
		{"missing-revision", "lifecycle.json", func(d map[string]any) { delete(d, "revision") }, domain.ErrInvalid},
		{"missing-interrupted", "lifecycle.json", func(d map[string]any) { delete(d, "interrupted") }, domain.ErrInvalid},
		{"null-shutdowns", "lifecycle.json", func(d map[string]any) { d["shutdowns"] = nil }, domain.ErrInvalid},
		{"unknown-v1-field", "lifecycle.json", func(d map[string]any) { d["new_field"] = true }, domain.ErrInvalid},
		{"same-previous-epoch", "lifecycle.json", func(d map[string]any) { d["previous_epoch"] = d["epoch"] }, domain.ErrInvalid},
		{"empty-current-epoch", "lifecycle.json", func(d map[string]any) { d["epoch"] = "" }, domain.ErrInvalid},
		{"interruption-no-uncertainty", "lifecycle.json", func(d map[string]any) {
			r := d["interrupted"].([]any)[0].(map[string]any)
			r["process_unknown"] = false
			r["pending_controls"] = 0
		}, domain.ErrInvalid},
		{"interruption-overflow", "lifecycle.json", func(d map[string]any) { d["interrupted"].([]any)[0].(map[string]any)["pending_controls"] = 1025 }, domain.ErrInvalid},
		{"interruption-duplicate", "lifecycle.json", func(d map[string]any) { r := d["interrupted"].([]any)[0]; d["interrupted"] = []any{r, r} }, domain.ErrInvalid},
		{"interruption-foreign-binding", "lifecycle.json", func(d map[string]any) {
			d["interrupted"].([]any)[0].(map[string]any)["selection"].(map[string]any)["generation"] = strings.Repeat("e", 32)
		}, domain.ErrInvalid},
		{"shutdown-duplicate", "lifecycle.json", func(d map[string]any) { r := d["shutdowns"].([]any)[0]; d["shutdowns"] = []any{r, r} }, domain.ErrInvalid},
		{"shutdown-not-quiescent", "lifecycle.json", func(d map[string]any) {
			d["shutdowns"].([]any)[0].(map[string]any)["binding"].(map[string]any)["pending_controls"] = 1
		}, domain.ErrInvalid},
		{"shutdown-draining-incomplete", "lifecycle.json", func(d map[string]any) { d["shutdowns"].([]any)[0].(map[string]any)["outcome"] = "draining" }, domain.ErrInvalid},
		{"shutdown-noncanonical-revision", "lifecycle.json", func(d map[string]any) {
			d["shutdowns"].([]any)[0].(map[string]any)["binding"].(map[string]any)["revision"] = "01"
		}, domain.ErrInvalid},
		{"update-zero-start", "lifecycle.json", func(d map[string]any) { d["update"].(map[string]any)["started"] = "0001-01-01T00:00:00Z" }, domain.ErrInvalid},
		{"update-noncanonical-start", "lifecycle.json", func(d map[string]any) { d["update"].(map[string]any)["started"] = "2026-11-01T00:00:00+00:00" }, domain.ErrInvalid},
		{"update-executable", "lifecycle.json", func(d map[string]any) { d["update"].(map[string]any)["version"] = "run /tmp/update" }, domain.ErrInvalid},
		{"update-protocol-overflow", "lifecycle.json", func(d map[string]any) { d["update"].(map[string]any)["protocol"] = 65536 }, domain.ErrInvalid},
		{"endpoint-root", "endpoints.json", func(d map[string]any) { d["root_witness"] = "other" }, domain.ErrInvalid},
		{"endpoint-revision-zero", "endpoints.json", func(d map[string]any) { d["revision"] = 0 }, domain.ErrInvalid},
		{"endpoint-same-identity", "endpoints.json", func(d map[string]any) {
			e := d["endpoints"].([]any)
			e[1].(map[string]any)["ino"] = e[0].(map[string]any)["ino"]
		}, domain.ErrInvalid},
		{"endpoint-wrong-order", "endpoints.json", func(d map[string]any) { e := d["endpoints"].([]any); d["endpoints"] = []any{e[1], e[0]} }, domain.ErrInvalid},
		{"endpoint-invalid-nsec", "endpoints.json", func(d map[string]any) { d["endpoints"].([]any)[0].(map[string]any)["ctime_nsec"] = 1e9 }, domain.ErrInvalid},
		{"endpoint-noncanonical-inode", "endpoints.json", func(d map[string]any) { d["endpoints"].([]any)[0].(map[string]any)["ino"] = "02" }, domain.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, root, _ := metadataLifecycleFixture(t)
			d := readMetadataFixture(t, root, tc.file)
			tc.mutate(d)
			writeMetadataFixture(t, root, tc.file, d)
			before, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			if _, err := m.Export(context.Background()); !errors.Is(err, tc.want) {
				t.Fatal("wrong schema refusal", err)
			}
			after, _ := os.ReadFile(filepath.Join(root, "receipts.json"))
			if !bytes.Equal(before, after) {
				t.Fatal("schema refusal changed history")
			}
		})
	}
}

func TestMetadataLifecyclePreviewBindsRetainedEvidenceAndReservesAllRevisions(t *testing.T) {
	for _, name := range []string{"lifecycle-policy", "endpoint-identity", "lifecycle-overflow", "execution-overflow", "receipts-overflow", "same-bytes-new-inode"} {
		t.Run(name, func(t *testing.T) {
			m, root, _ := metadataLifecycleFixture(t)
			ctx := context.Background()
			p, err := m.PreviewForget(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target := "lifecycle.json"
			if name == "endpoint-identity" {
				target = "endpoints.json"
			}
			if name == "execution-overflow" {
				target = "execution.json"
			}
			if name == "receipts-overflow" {
				target = "receipts.json"
			}
			d := readMetadataFixture(t, root, target)
			switch name {
			case "lifecycle-policy":
				d["autostart_opt_in"] = false
			case "endpoint-identity":
				d["endpoints"].([]any)[0].(map[string]any)["ctime_sec"] = 2
			case "lifecycle-overflow", "execution-overflow", "receipts-overflow":
				d["revision"] = json.Number("18446744073709551615")
			}
			if name == "same-bytes-new-inode" {
				path := filepath.Join(root, target)
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(path+".fixture", b, 0600); e != nil {
					t.Fatal(e)
				}
				if e = os.Rename(path+".fixture", path); e != nil {
					t.Fatal(e)
				}
			} else {
				writeMetadataFixture(t, root, target, d)
			}
			want := domain.ErrChanged
			if strings.HasSuffix(name, "overflow") {
				p, err = m.PreviewForget(ctx)
				if err != nil {
					t.Fatal(err)
				}
				want = domain.ErrLimit
			}
			c, err := domain.MetadataForgetConfirmationOf(p.Snapshot.Digest, true, true)
			if err != nil {
				t.Fatal(err)
			}
			before := make(map[string][]byte)
			for _, f := range []string{"receipts.json", "lifecycle.json", "execution.json"} {
				before[f], err = os.ReadFile(filepath.Join(root, f))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err = m.Forget(ctx, c); !errors.Is(err, want) {
				t.Fatal("invalid preview/revision accepted", err)
			}
			for f, b := range before {
				a, e := os.ReadFile(filepath.Join(root, f))
				if e != nil || !bytes.Equal(a, b) {
					t.Fatal("preflight refusal partially mutated", f, e)
				}
			}
		})
	}
}

func TestMetadataLifecycleAccountsAggregateAndPerEnvelopeBudgets(t *testing.T) {
	for _, kind := range []string{"aggregate", "endpoint", "lifecycle"} {
		t.Run(kind, func(t *testing.T) {
			m, root, _ := metadataLifecycleFixture(t)
			var err error
			if kind == "aggregate" {
				var total int64
				for _, name := range []string{"registry.json", "execution.json", "receipts.json", "lifecycle.json", "endpoints.json"} {
					info, e := os.Stat(filepath.Join(root, name))
					if e != nil {
						t.Fatal(e)
					}
					total += info.Size()
				}
				m, err = infra.OpenMetadataStore(root, total-1)
				if err != nil {
					t.Fatal(err)
				}
			} else {
				name := "endpoints.json"
				limit := 4096
				if kind == "lifecycle" {
					name = "lifecycle.json"
					limit = 16 * 1024 * 1024
				}
				path := filepath.Join(root, name)
				b, e := os.ReadFile(path)
				if e != nil {
					t.Fatal(e)
				}
				b = append(b, bytes.Repeat([]byte(" "), limit+1-len(b))...)
				if e = os.WriteFile(path, b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if _, err = m.Export(context.Background()); !errors.Is(err, domain.ErrLimit) {
				t.Fatal("metadata budget omitted sidecar", err)
			}
		})
	}
}
