package sqlite

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func TestPayloadCodecMechanicalInventory(t *testing.T) {
	t.Parallel()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", ".."))
	codecHits := grepLive(t, root, regexp.MustCompile(`PayloadCodec|payload_codec`), []string{"domain", "application", "presentation", "infrastructure"})
	if len(codecHits) != 0 {
		t.Fatalf("PayloadCodec|payload_codec live hits:\n%s", strings.Join(codecHits, "\n"))
	}
	rehearsalHits := grepLive(t, root, regexp.MustCompile(`payload_rehearsal|PayloadRehearsal`), []string{"domain", "application", "presentation", "infrastructure"})
	if len(rehearsalHits) != 0 {
		t.Fatalf("payload_rehearsal|PayloadRehearsal live hits:\n%s", strings.Join(rehearsalHits, "\n"))
	}
	wantCodecColumns := map[string]struct{}{
		"infrastructure/sqlite/restore_dedupe_archive.go": {},
		"infrastructure/sqlite/canonical_event_audit.go":  {},
		"infrastructure/sqlite/attestation_store.go":      {},
		"domain/archive_segment.go":                       {},
	}
	columnHits := grepLive(t, root, regexp.MustCompile(`body_codec|command_codec`), []string{"domain", "application", "presentation", "infrastructure"})
	got := map[string]struct{}{}
	for _, hit := range columnHits {
		file := strings.SplitN(hit, ":", 2)[0]
		got[file] = struct{}{}
		if _, ok := wantCodecColumns[file]; !ok {
			t.Errorf("unexpected body_codec|command_codec hit: %s", hit)
		}
	}
	for file := range wantCodecColumns {
		if _, ok := got[file]; !ok {
			t.Errorf("missing expected body_codec|command_codec file %s", file)
		}
	}
}

func grepLive(t *testing.T, root string, pattern *regexp.Regexp, dirs []string) []string {
	t.Helper()
	var hits []string
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			if strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "/migrations/") || strings.Contains(filepath.Base(rel), "_migration") {
				return nil
			}
			if !strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, ".sql") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %s: %w", rel, err)
			}
			for i, line := range bytes.Split(body, []byte("\n")) {
				if pattern.Match(line) {
					hits = append(hits, rel+":"+itoa(i+1)+": "+string(line))
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return hits
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestMigrationDecoderUnreachableFromRuntimePaths(t *testing.T) {
	t.Parallel()
	decoderSymbols := map[string]struct{}{
		"decodePayload":                      {},
		"payloadRow":                         {},
		"encodedPayload":                     {},
		"storedBodyArg":                      {},
		"payloadCodecIdentity":               {},
		"archiveBodyArg":                     {},
		"decodePayloadsForMigrationOrRefuse": {},
	}
	allowedDecoder := map[string]struct{}{
		"payload_codec_migration.go":           {},
		"drop_encoded_payloads.go":             {},
		"canonical_event_audit.go":             {},
		"attestation_store.go":                 {},
		"restore_dedupe_archive.go":            {},
		"decode_payloads.go":                   {},
		"prepared_upgrade_migration_recipe.go": {},
	}
	negative := map[string]struct{}{
		"event_delivery_store.go":           {},
		"payload_hydration.go":              {},
		"command_audit_payload.go":          {},
		"event_search_two_tier_fallback.go": {},
		"session_datasource.go":             {},
		"compaction_copy_filter.go":         {},
		"compaction_sqlite.go":              {},
		"compaction_files.go":               {},
		"bundle_datasource.go":              {},
		"store_archive.go":                  {},
		"event_datasource.go":               {},
		"store_management_datasource.go":    {},
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if !ok {
				return true
			}
			if _, watched := decoderSymbols[ident.Name]; !watched {
				return true
			}
			if _, allowed := allowedDecoder[name]; !allowed {
				t.Errorf("%s references decoder symbol %s", name, ident.Name)
			}
			if _, forbidden := negative[name]; forbidden {
				t.Errorf("runtime path %s references decoder symbol %s", name, ident.Name)
			}
			return true
		})
	}
}
