package presentation

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/duck8823/traceary/application/redaction"
	"golang.org/x/xerrors"
)

// configFile mirrors the on-disk JSON layout. It is intentionally unexported
// because callers receive the loaded values through Config.
type configFile struct {
	Audit         auditSection         `json:"audit"`
	UI            uiSection            `json:"ui"`
	Redact        redactSection        `json:"redact"`
	Read          readSection          `json:"read"`
	Retention     retentionSection     `json:"retention"`
	Consolidation consolidationSection `json:"consolidation"`
	WakeInjection wakeInjectionSection `json:"wake_injection"`
	Compact       compactSection       `json:"compact"`
}

type compactSection struct {
	ReclaimWarnBytes *int64 `json:"reclaim_warn_bytes"`
}

type auditSection struct {
	MaxInputBytes  int `json:"max_input_bytes"`
	MaxOutputBytes int `json:"max_output_bytes"`
}

type uiSection struct {
	Language string `json:"language"`
}

type redactSection struct {
	ExtraPatterns []string               `json:"extra_patterns"`
	Rules         []redaction.RuleConfig `json:"rules"`
}

type readSection struct {
	Fields  []string                 `json:"fields"`
	Presets map[string]readPresetDoc `json:"presets"`
	Color   string                   `json:"color"`
}

// retentionSection configures optional archive-before-GC automation (#1372).
// Default mode is disabled (fail-closed): operators must opt into archive_then_gc.
type retentionSection struct {
	Mode          string                    `json:"mode"`
	ArchiveThenGC retentionArchiveThenGCDoc `json:"archive_then_gc"`
}

// consolidationSection configures the stop-hook consolidation pressure trigger
// (#1674). ThresholdBytes is a pointer so explicit 0 (disabled) is distinct
// from an absent key (default 64 KiB), the same way readPresetFilters.Failures
// distinguishes absent from explicit false.
type consolidationSection struct {
	ThresholdBytes *int64 `json:"threshold_bytes"`
}

// wakeInjectionSection configures the session-start wake injection budget
// (#1684). BudgetBytes is a pointer so explicit 0 (disabled) is distinct from
// an absent key (default 8 KiB), matching consolidationSection.
type wakeInjectionSection struct {
	BudgetBytes *int64 `json:"budget_bytes"`
}

type retentionArchiveThenGCDoc struct {
	Interval      string `json:"interval"`
	KeepDays      int    `json:"keep_days"`
	Target        string `json:"target"`
	OutputDir     string `json:"output_dir"`
	PassphraseEnv string `json:"passphrase_env"`
}

// readPresetDoc mirrors a user-defined read preset entry in config.json. The
// fields are intentionally loose so LoadConfig stays lenient — preset
// validation (unknown field names, unsupported kinds, etc.) happens when a
// preset is applied at command runtime.
type readPresetDoc struct {
	Fields  []string          `json:"fields"`
	Filters readPresetFilters `json:"filters"`
}

// readPresetFilters lists the filter keys a preset can carry. Only filters
// that every read command (tail / list / search) can consume are included.
type readPresetFilters struct {
	Kind      string `json:"kind"`
	Failures  *bool  `json:"failures"`
	Workspace string `json:"workspace"`
	SessionID string `json:"session_id"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
}

// Config carries the resolved configuration values consumed by the CLI and
// MCP server. Zero values mean "fall back to the built-in default" so callers
// do not need to distinguish between "file missing" and "key missing".
// Consolidation and WakeInjection are the exceptions: see ConsolidationConfig,
// WakeInjectionConfig, and LoadConfig.
type Config struct {
	// UILanguage is the operator-facing CLI/TUI language (en / ja). Empty
	// string means "fall back to the built-in default language". Runtime
	// environment overrides such as TRACEARY_LANG are resolved by the CLI
	// layer because they are process-local, not persisted config.
	UILanguage string
	// ExtraRedactPatterns are additional regex patterns applied on top of the
	// built-in audit redaction rules. Nil / empty means "no extras".
	ExtraRedactPatterns []string
	// StructuredRedactRules are named/configurable redaction rules applied
	// alongside ExtraRedactPatterns. Nil / empty means "no configured structured rules".
	StructuredRedactRules []redaction.RuleConfig
	// AuditMaxInputBytes and AuditMaxOutputBytes override the built-in
	// command-audit persistence limits when positive. Zero means "fall back to
	// the built-in default"; runtime command flags and environment variables
	// may still override these config defaults.
	AuditMaxInputBytes  int
	AuditMaxOutputBytes int
	// ReadFields is the default column order applied to tail / list / search
	// text output when the user does not pass --fields. Nil / empty means
	// "fall back to the built-in default column order".
	ReadFields []string
	// ReadPresets captures user-defined read presets. The runtime validates
	// field names, kind values, and other constraints when a preset is
	// applied; LoadConfig only parses the shape.
	ReadPresets map[string]ReadPreset
	// ReadColor is the default --color mode (auto / always / never) for
	// read commands. Empty string means "fall back to auto". The runtime
	// validates the value when a command is about to render text.
	ReadColor string
	// Retention holds opt-in archive-before-GC automation. Zero Mode means
	// disabled (same as explicit "disabled").
	Retention RetentionConfig
	// Consolidation holds the stop-hook pressure threshold. LoadConfig always
	// resolves ThresholdBytes: default 64 KiB when the file/key is absent;
	// explicit 0 disables; unreadable or malformed config also resolves to 0
	// so a broken file cannot re-enable a trigger the operator turned off.
	Consolidation ConsolidationConfig
	// WakeInjection holds the session-start summary injection budget.
	// LoadConfig always resolves BudgetBytes: default 8 KiB when the file/key
	// is absent; explicit 0 disables; unreadable or malformed config also
	// resolves to 0 so a broken file cannot re-enable injection.
	WakeInjection WakeInjectionConfig
	// Compact holds the reclaim warning threshold. Unlike consolidation and
	// wake injection, a malformed / unreadable config falls back to the
	// published default so the warning still fires. A warning has no side
	// effect; hiding it because the file is broken would let the store grow
	// with nobody told.
	Compact CompactConfig
}

// RetentionModeDisabled is the fail-closed default for automatic archive-then-gc.
const RetentionModeDisabled = "disabled"

// RetentionModeArchiveThenGC opts into opportunistic archive-before-GC (#1372).
const RetentionModeArchiveThenGC = "archive_then_gc"

// DefaultConsolidationThresholdBytes is the stop-hook pressure threshold when
// consolidation.threshold_bytes is absent from config.json (64 KiB).
const DefaultConsolidationThresholdBytes int64 = 64 * 1024

// DefaultWakeInjectionBudgetBytes is the wake-injection stdout budget when
// wake_injection.budget_bytes is absent from config.json (8 KiB).
const DefaultWakeInjectionBudgetBytes int64 = 8192

// DefaultCompactReclaimWarnBytes is the reclaim warning threshold when
// compact.reclaim_warn_bytes is absent from config.json (1 GiB).
//
// Deliberately inverted from consolidation / wake injection: those features
// disable (0) when the file is unreadable so a broken config cannot re-enable
// a trigger. This warning has no side effect, so a broken file still uses the
// default and still warns.
const DefaultCompactReclaimWarnBytes int64 = 1 << 30

// RetentionConfig is the runtime view of config.json retention.
type RetentionConfig struct {
	// Mode is "disabled" (default) or "archive_then_gc".
	Mode string
	// Interval between automatic archive-then-gc attempts (e.g. "168h").
	Interval string
	// KeepDays matches store gc --keep-days when positive; zero means default 90.
	KeepDays int
	// Target is events|sessions|memories|memory_edges|all; empty means all.
	Target string
	// OutputDir stores archive packages; empty means ~/.config/traceary/archives.
	OutputDir string
	// PassphraseEnv is the name of an env var holding an optional passphrase.
	// Secrets are never stored in config or SQLite.
	PassphraseEnv string
}

// ConsolidationConfig is the runtime view of config.json consolidation.
type ConsolidationConfig struct {
	// ThresholdBytes is the unrefined body-byte sum that triggers a stop-hook
	// consolidation request. Explicit 0 disables the trigger. When the config
	// file or key is absent, LoadConfig sets DefaultConsolidationThresholdBytes.
	// When the file is present but unusable (unreadable / malformed), LoadConfig
	// sets 0 so the trigger stays off rather than re-applying the default.
	ThresholdBytes int64
}

// WakeInjectionConfig is the runtime view of config.json wake_injection.
type WakeInjectionConfig struct {
	// BudgetBytes is the maximum number of bytes written to stdout for finished
	// session summaries at wake. Explicit 0 disables injection. When the config
	// file or key is absent, LoadConfig sets DefaultWakeInjectionBudgetBytes.
	// When the file is present but unusable (unreadable / malformed), LoadConfig
	// sets 0 so injection stays off rather than re-applying the default.
	// Negative values are treated as disabled by the injection path.
	BudgetBytes int64
}

// CompactConfig is the runtime view of config.json compact.
type CompactConfig struct {
	// ReclaimWarnBytes is the estimated reclaimable size that triggers a
	// doctor / non-hook stderr warning. Explicit 0 disables the warning.
	// Absent key and unusable files resolve to DefaultCompactReclaimWarnBytes.
	ReclaimWarnBytes int64
}

// ReadPreset is the runtime-facing view of a user-defined preset loaded from
// config.json. It intentionally uses plain fields so callers can apply a
// preset without importing JSON tag types from this package.
type ReadPreset struct {
	Fields  []string
	Filters ReadPresetFilters
}

// ReadPresetFilters lists the filter keys a preset can carry. Presence
// (non-zero value) is what matters to the runtime; the preset applies the
// filter only when the corresponding key is set.
type ReadPresetFilters struct {
	Kind      string
	Failures  *bool
	Workspace string
	SessionID string
	Client    string
	Agent     string
}

// configLoadStatus reports how loadConfigFile resolved the on-disk file so
// LoadConfig can treat "operator never configured" differently from "file
// exists but is unusable" for consolidation and wake injection.
type configLoadStatus int

const (
	// configLoadAbsent: no config.json directory entry at all. Operator
	// expressed nothing. A dangling symlink is NOT absent — Lstat sees the
	// entry even when ReadFile's follow yields ENOENT.
	configLoadAbsent configLoadStatus = iota
	// configLoadUnusable: path unresolvable (including dangling symlink),
	// read error, or malformed JSON. Operator intent is unknown;
	// consolidation and wake injection must not fire on defaults.
	configLoadUnusable
	// configLoadOK: file parsed successfully.
	configLoadOK
)

// LoadConfig reads the optional Traceary config file and returns a Config.
// For most fields, a missing / unreadable / invalid file yields zero values
// that fall back to built-in defaults, and a warning is logged via slog.
// Consolidation and wake injection are special: absent file → published
// defaults; present but unusable → thresholds/budgets 0 (disabled) so a broken
// config cannot re-enable a feature the operator may have turned off.
func LoadConfig() Config {
	file, status := loadConfigFile()
	if file == nil {
		cfg := Config{}
		switch status {
		case configLoadUnusable:
			// Keep every other field at zero (built-in defaults). Only
			// consolidation and wake injection disable: firing on an unknown
			// configuration can re-enable a feature set to 0.
			cfg.Consolidation = ConsolidationConfig{ThresholdBytes: 0}
			cfg.WakeInjection = WakeInjectionConfig{BudgetBytes: 0}
			// Warning polarity is inverted: still use the published default.
			cfg.Compact = CompactConfig{ReclaimWarnBytes: DefaultCompactReclaimWarnBytes}
		default:
			// Absent (and any unexpected nil-file status): operator never
			// configured Traceary, so the published defaults apply.
			cfg.Consolidation = toConsolidationConfig(consolidationSection{})
			cfg.WakeInjection = toWakeInjectionConfig(wakeInjectionSection{})
			cfg.Compact = toCompactConfig(compactSection{})
		}
		return cfg
	}
	return Config{
		AuditMaxInputBytes:    file.Audit.MaxInputBytes,
		AuditMaxOutputBytes:   file.Audit.MaxOutputBytes,
		UILanguage:            file.UI.Language,
		ExtraRedactPatterns:   file.Redact.ExtraPatterns,
		StructuredRedactRules: file.Redact.Rules,
		ReadFields:            file.Read.Fields,
		ReadPresets:           toReadPresetMap(file.Read.Presets),
		ReadColor:             file.Read.Color,
		Retention:             toRetentionConfig(file.Retention),
		Consolidation:         toConsolidationConfig(file.Consolidation),
		WakeInjection:         toWakeInjectionConfig(file.WakeInjection),
		Compact:               toCompactConfig(file.Compact),
	}
}

func toCompactConfig(raw compactSection) CompactConfig {
	if raw.ReclaimWarnBytes == nil {
		return CompactConfig{ReclaimWarnBytes: DefaultCompactReclaimWarnBytes}
	}
	return CompactConfig{ReclaimWarnBytes: *raw.ReclaimWarnBytes}
}

func toRetentionConfig(raw retentionSection) RetentionConfig {
	mode := strings.TrimSpace(raw.Mode)
	if mode == "" {
		mode = RetentionModeDisabled
	}
	return RetentionConfig{
		Mode:          mode,
		Interval:      strings.TrimSpace(raw.ArchiveThenGC.Interval),
		KeepDays:      raw.ArchiveThenGC.KeepDays,
		Target:        strings.TrimSpace(raw.ArchiveThenGC.Target),
		OutputDir:     strings.TrimSpace(raw.ArchiveThenGC.OutputDir),
		PassphraseEnv: strings.TrimSpace(raw.ArchiveThenGC.PassphraseEnv),
	}
}

func toConsolidationConfig(raw consolidationSection) ConsolidationConfig {
	if raw.ThresholdBytes == nil {
		return ConsolidationConfig{ThresholdBytes: DefaultConsolidationThresholdBytes}
	}
	// Explicit zero disables; any other value is used as-is (including negative,
	// which the use case treats as disabled the same way).
	return ConsolidationConfig{ThresholdBytes: *raw.ThresholdBytes}
}

func toWakeInjectionConfig(raw wakeInjectionSection) WakeInjectionConfig {
	if raw.BudgetBytes == nil {
		return WakeInjectionConfig{BudgetBytes: DefaultWakeInjectionBudgetBytes}
	}
	// Explicit zero disables; any other value is used as-is (including negative,
	// which the wake path treats as disabled the same way).
	return WakeInjectionConfig{BudgetBytes: *raw.BudgetBytes}
}

func toReadPresetMap(raw map[string]readPresetDoc) map[string]ReadPreset {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]ReadPreset, len(raw))
	for name, doc := range raw {
		out[name] = ReadPreset{
			Fields: append([]string(nil), doc.Fields...),
			Filters: ReadPresetFilters{
				Kind:      doc.Filters.Kind,
				Failures:  doc.Filters.Failures,
				Workspace: doc.Filters.Workspace,
				SessionID: doc.Filters.SessionID,
				Client:    doc.Filters.Client,
				Agent:     doc.Filters.Agent,
			},
		}
	}
	return out
}

// LoadExtraRedactPatterns preserves the earlier API so callers that only need
// redaction patterns can keep using this single-purpose helper.
func LoadExtraRedactPatterns() []string {
	return LoadConfig().ExtraRedactPatterns
}

func loadConfigFile() (*configFile, configLoadStatus) {
	configPath, err := DefaultConfigPath()
	if err != nil {
		slog.Warn(
			"Traceary config path could not be resolved; config-backed features fall back to built-in defaults",
			"error", err,
		)
		return nil, configLoadUnusable
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			// ReadFile follows symlinks. A dangling symlink reports ENOENT
			// even though the directory entry exists; Lstat does not follow,
			// so it separates "operator never configured" from "configured
			// path is unresolvable". Treating the latter as absent would
			// re-enable the default 64 KiB consolidation trigger — the same
			// failure mode as a malformed / unreadable file.
			if _, lstatErr := os.Lstat(configPath); !os.IsNotExist(lstatErr) {
				slog.Warn(
					"Traceary config could not be read; config-backed features fall back to built-in defaults until the file is readable: "+configPath,
					"error", err,
				)
				return nil, configLoadUnusable
			}
			return nil, configLoadAbsent
		}
		slog.Warn(
			"Traceary config could not be read; config-backed features fall back to built-in defaults until the file is readable: "+configPath,
			"error", err,
		)
		return nil, configLoadUnusable
	}

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		slog.Warn(
			"Traceary config is invalid; config-backed features fall back to built-in defaults until the file is fixed: "+configPath,
			"error", err,
		)
		return nil, configLoadUnusable
	}

	return &file, configLoadOK
}

// DefaultConfigPath returns the canonical per-user Traceary config path.
func DefaultConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "config.json"), nil
}
