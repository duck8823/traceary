package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// probePattern matches the issue mechanical grep plus the extended
// spellings the issue grep misses (tableHasColumn, transactionColumnExists,
// sqliteIndexExists).
var probePattern = regexp.MustCompile(`tableExists|columnExists|indexExists|hasCodec|Supported\(|tableHasColumn|transactionColumnExists|sqliteIndexExists`)

type probeAllowEntry struct {
	class   string
	site    string
	verdict string
	reason  string
}

// recordedCapabilityProbes is the item-1 inventory and the item-5
// allow-list. Adding a probe without an entry fails go test.
var recordedCapabilityProbes = map[string]probeAllowEntry{
	"infrastructure/sqlite/attestation_store.go:207":             {class: "integrity", site: "write path", verdict: "kept", reason: "optional feature facet: attestation_links absence means skip, not a version era"},
	"infrastructure/sqlite/bound_drop.go:100":                    {class: "integrity", site: "sibling-offline 079", verdict: "carved-out", reason: "sibling-offline conservation of usage_observation_runs"},
	"infrastructure/sqlite/bound_drop.go:104":                    {class: "integrity", site: "sibling-offline 079", verdict: "carved-out", reason: "sibling-offline conservation of usage_observation_runs"},
	"infrastructure/sqlite/bound_drop.go:18":                     {class: "integrity", site: "sibling-offline 079", verdict: "carved-out", reason: "sibling-offline refuse/verify for 079"},
	"infrastructure/sqlite/bound_drop.go:93":                     {class: "integrity", site: "sibling-offline 079", verdict: "carved-out", reason: "sibling-offline refuse/verify for 079"},
	"infrastructure/sqlite/canonical_event_audit.go:121":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "082 upgrade verification reads source while body_codec still exists"},
	"infrastructure/sqlite/canonical_event_audit.go:125":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "hasCodec argument to canonicalEvents on codec-era source"},
	"infrastructure/sqlite/canonical_event_audit.go:129":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "hasCodec argument to canonicalAudits on codec-era source"},
	"infrastructure/sqlite/canonical_event_audit.go:142":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "canonicalEvents signature for codec-era source"},
	"infrastructure/sqlite/canonical_event_audit.go:144":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "canonicalEvents codec branch"},
	"infrastructure/sqlite/canonical_event_audit.go:175":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "canonicalAudits signature for codec-era source"},
	"infrastructure/sqlite/canonical_event_audit.go:177":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "canonicalAudits codec branch"},
	"infrastructure/sqlite/canonical_event_audit.go:195":         {class: "integrity", site: "offline 082", verdict: "kept", reason: "canonicalAudits codec metadata slice"},
	"infrastructure/sqlite/canonical_event_audit.go:289":         {class: "integrity", site: "definition", verdict: "kept", reason: "tableHasColumn helper definition"},
	"infrastructure/sqlite/compaction_copy_filter.go:100":        {class: "integrity", site: "definition", verdict: "kept", reason: "tableExists helper definition"},
	"infrastructure/sqlite/compaction_rollback_guard.go:39":      {class: "integrity", site: "rollback guard", verdict: "kept", reason: "rollback guard reads only the events table presence on both inodes; no schema-version branch"},
	"infrastructure/sqlite/compaction_copy_filter.go:199":        {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: events table"},
	"infrastructure/sqlite/compaction_copy_filter.go:203":        {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: command_audits table"},
	"infrastructure/sqlite/compaction_copy_filter.go:67":         {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: source may not be a Traceary store"},
	"infrastructure/sqlite/compaction_exchange_darwin.go:11":     {class: "platform", site: "definition", verdict: "kept", reason: "atomicExchangeSupported platform helper"},
	"infrastructure/sqlite/compaction_exchange_linux.go:11":      {class: "platform", site: "definition", verdict: "kept", reason: "atomicExchangeSupported platform helper"},
	"infrastructure/sqlite/compaction_exchange_unsupported.go:8": {class: "platform", site: "definition", verdict: "kept", reason: "atomicExchangeSupported platform helper"},
	"infrastructure/sqlite/compaction_files.go:568":              {class: "platform", site: "offline exchange", verdict: "kept", reason: "atomic file exchange is a platform capability, not schema compatibility"},
	"infrastructure/sqlite/compaction_sqlite.go:132":             {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: events"},
	"infrastructure/sqlite/compaction_sqlite.go:136":             {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: events"},
	"infrastructure/sqlite/compaction_verify_tables.go:64":       {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: command_audits"},
	"infrastructure/sqlite/compaction_verify_tables.go:68":       {class: "integrity", site: "offline copy", verdict: "kept", reason: "copy-path structural invariant: command_audits"},
	"infrastructure/sqlite/decode_payloads.go:45":                {class: "integrity", site: "sibling-offline 082", verdict: "carved-out", reason: "081/082 decode path still inspects body_codec before 082 drops it"},
	"infrastructure/sqlite/drop_archive_segments.go:33":          {class: "integrity", site: "sibling-offline 084", verdict: "carved-out", reason: "sibling-offline refuse/verify for 084"},
	"infrastructure/sqlite/drop_archive_segments.go:51":          {class: "integrity", site: "sibling-offline 084", verdict: "carved-out", reason: "sibling-offline refuse/verify for 084"},
	"infrastructure/sqlite/drop_body_retention.go:158":           {class: "integrity", site: "sibling-offline 083", verdict: "carved-out", reason: "sibling-offline verify for 083"},
	"infrastructure/sqlite/drop_body_retention.go:167":           {class: "integrity", site: "sibling-offline 083", verdict: "carved-out", reason: "sibling-offline verify for 083"},
	"infrastructure/sqlite/drop_body_retention.go:175":           {class: "integrity", site: "sibling-offline 083", verdict: "carved-out", reason: "sibling-offline verify for 083"},
	"infrastructure/sqlite/drop_body_retention.go:179":           {class: "false-positive", site: "sibling-offline 083", verdict: "kept", reason: "local variable indexExists, not a probe helper"},
	"infrastructure/sqlite/drop_body_retention.go:51":            {class: "integrity", site: "sibling-offline 083", verdict: "carved-out", reason: "sibling-offline inspect for 083"},
	"infrastructure/sqlite/drop_compat_surface.go:37":            {class: "integrity", site: "offline 086", verdict: "carved-out", reason: "086 refuse/verify of legacy_source_hook"},
	"infrastructure/sqlite/drop_compat_surface.go:55":            {class: "integrity", site: "offline 086", verdict: "carved-out", reason: "086 refuse/verify of legacy_source_hook"},
	"infrastructure/sqlite/drop_dedupe_archive.go:127":           {class: "integrity", site: "sibling-offline 081", verdict: "carved-out", reason: "sibling-offline verifier for 081"},
	"infrastructure/sqlite/drop_dedupe_archive.go:17":            {class: "integrity", site: "sibling-offline 081", verdict: "carved-out", reason: "sibling-offline verifier for 081"},
	"infrastructure/sqlite/drop_dedupe_archive.go:96":            {class: "integrity", site: "sibling-offline 081", verdict: "carved-out", reason: "sibling-offline verifier for 081"},
	"infrastructure/sqlite/drop_encoded_payloads.go:157":         {class: "integrity", site: "sibling-offline 082", verdict: "carved-out", reason: "sibling-offline verifier; 082 still sees body_codec on the source"},
	"infrastructure/sqlite/drop_encoded_payloads.go:186":         {class: "integrity", site: "sibling-offline 082", verdict: "carved-out", reason: "sibling-offline verifier for 082"},
	"infrastructure/sqlite/drop_memory_edges.go:33":              {class: "integrity", site: "sibling-offline 085", verdict: "carved-out", reason: "sibling-offline refuse/verify for 085"},
	"infrastructure/sqlite/drop_memory_edges.go:51":              {class: "integrity", site: "sibling-offline 085", verdict: "carved-out", reason: "sibling-offline refuse/verify for 085"},
	"infrastructure/sqlite/drop_search_projection_family.go:37":  {class: "integrity", site: "sibling-offline 080", verdict: "carved-out", reason: "sibling-offline verifier for 080"},
	"infrastructure/sqlite/drop_search_projection_family.go:45":  {class: "integrity", site: "sibling-offline 080", verdict: "carved-out", reason: "sibling-offline verifier for 080"},
	"infrastructure/sqlite/event_datasource.go:1204":             {class: "integrity", site: "read path", verdict: "kept", reason: "feature facet: optional output_metadata on historical command_audits"},
	"infrastructure/sqlite/event_delivery_store.go:218":          {class: "integrity", site: "write path", verdict: "kept", reason: "feature facet: command_audits.output_metadata from 077; optional write of metadata"},
	"infrastructure/sqlite/event_delivery_store.go:240":          {class: "integrity", site: "definition", verdict: "kept", reason: "transactionColumnExists helper definition"},
	"infrastructure/sqlite/event_delivery_store.go:397":          {class: "integrity", site: "write path", verdict: "kept", reason: "076 is data-dependent offline; live open still writes the pre-collapse observation row until doctor --fix applies 076"},
	"infrastructure/sqlite/event_delivery_store.go:539":          {class: "integrity", site: "definition", verdict: "kept", reason: "columnExistsInTransaction helper definition"},
	"infrastructure/sqlite/event_delivery_store.go:552":          {class: "integrity", site: "definition", verdict: "kept", reason: "tableExistsInTransaction helper definition"},
	"infrastructure/sqlite/prepared_migration_catalog.go:357":    {class: "false-positive", site: "catalog", verdict: "kept", reason: "local variable tableExists, not a probe helper"},
	"infrastructure/sqlite/prepared_migration_catalog.go:358":    {class: "integrity", site: "bootstrap", verdict: "kept", reason: "schema_migrations presence is the catalog bootstrap invariant"},
	"infrastructure/sqlite/prepared_migration_catalog.go:361":    {class: "false-positive", site: "catalog", verdict: "kept", reason: "local variable tableExists, not a probe helper"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:125":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "five-table conservation: source may lack a table"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:129":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "five-table conservation: candidate may lack a table"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:196":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "index conservation law"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:203":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "index conservation base table"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:207":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "index conservation base table"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:236":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "collapse-76 conservation of session_workspace_observations"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:240":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "collapse-76 conservation of session_workspace_observations"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:294":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "collapse-76 index presence"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:305":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "collapse-76 dropped index absence"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:317":     {class: "integrity", site: "definition", verdict: "kept", reason: "sqliteIndexExists helper definition"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:326":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "usage_observations conservation (epoch-zero repair)"},
	"infrastructure/sqlite/prepared_upgrade_verifier.go:330":     {class: "integrity", site: "offline verify", verdict: "kept", reason: "usage_observations conservation (epoch-zero repair)"},
	"infrastructure/sqlite/restore_dedupe_archive.go:108":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec parameter of the 081 restore page writer"},
	"infrastructure/sqlite/restore_dedupe_archive.go:131":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec argument of the 081 page-row insert"},
	"infrastructure/sqlite/restore_dedupe_archive.go:200":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec parameter of the 081 archive page loader"},
	"infrastructure/sqlite/restore_dedupe_archive.go:203":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec branch of the 081 archive page query"},
	"infrastructure/sqlite/restore_dedupe_archive.go:222":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec branch of the 081 archive page scan"},
	"infrastructure/sqlite/restore_dedupe_archive.go:248":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec parameter of the 081 restore insert"},
	"infrastructure/sqlite/restore_dedupe_archive.go:251":        {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec branch of the 081 restore insert"},
	"infrastructure/sqlite/restore_dedupe_archive.go:39":         {class: "integrity", site: "sibling-offline 081", verdict: "carved-out", reason: "081 restore inspects the archive table before 082"},
	"infrastructure/sqlite/restore_dedupe_archive.go:56":         {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "081 executes before 082 so body_codec is still present at restore; autocommit tableHasColumn probe"},
	"infrastructure/sqlite/restore_dedupe_archive.go:64":         {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec argument threading into the paged 081 restore"},
	"infrastructure/sqlite/restore_dedupe_archive.go:81":         {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec parameter of the 081 paged restore driver"},
	"infrastructure/sqlite/restore_dedupe_archive.go:85":         {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec argument into the 081 archive page loader"},
	"infrastructure/sqlite/restore_dedupe_archive.go:92":         {class: "integrity", site: "sibling-offline 081", verdict: "kept", reason: "hasCodec argument into the 081 archive page writer"},
	"presentation/cli/memory_activate.go:264":                    {class: "platform", site: "cli apply", verdict: "kept", reason: "host-specific memory-activate apply; not a store schema probe"},
	"presentation/cli/memory_activate.go:276":                    {class: "platform", site: "definition", verdict: "kept", reason: "helper definition for memoryActivationApplySupported"},
}

func TestCapabilityProbesAreAllowListed(t *testing.T) {
	t.Parallel()
	found := map[string]bool{}
	err := filepath.WalkDir(".", func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, readErr := os.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s: %w", path, readErr)
		}
		for i, line := range strings.Split(string(contents), "\n") {
			if !probePattern.MatchString(line) {
				continue
			}
			rel := filepath.ToSlash(path)
			rel = strings.TrimPrefix(rel, "./")
			key := rel + ":" + strconv.Itoa(i+1)
			found[key] = true
			if _, ok := recordedCapabilityProbes[key]; !ok {
				t.Errorf("unrecorded capability probe %s: %s", key, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk .: %v", err)
	}
	for key, entry := range recordedCapabilityProbes {
		if entry.reason == "" {
			t.Errorf("%s has no stated reason", key)
		}
		if !found[key] {
			t.Errorf("stale allow-list entry %s", key)
		}
	}
}
