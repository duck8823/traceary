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
	Audit     auditSection     `json:"audit"`
	UI        uiSection        `json:"ui"`
	Redact    redactSection    `json:"redact"`
	Read      readSection      `json:"read"`
	Retention retentionSection `json:"retention"`
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
}

// RetentionModeDisabled is the fail-closed default for automatic archive-then-gc.
const RetentionModeDisabled = "disabled"

// RetentionModeArchiveThenGC opts into opportunistic archive-before-GC (#1372).
const RetentionModeArchiveThenGC = "archive_then_gc"

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

// LoadConfig reads the optional Traceary config file and returns a Config.
// When the file is missing, unreadable, or invalid, the returned Config is
// zero-valued and a warning is logged via slog so operators can see that
// config-backed features fell back to built-in defaults.
func LoadConfig() Config {
	file := loadConfigFile()
	if file == nil {
		return Config{}
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
	}
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

func loadConfigFile() *configFile {
	configPath, err := DefaultConfigPath()
	if err != nil {
		slog.Warn(
			"Traceary config path could not be resolved; config-backed features fall back to built-in defaults",
			"error", err,
		)
		return nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		slog.Warn(
			"Traceary config could not be read; config-backed features fall back to built-in defaults until the file is readable: "+configPath,
			"error", err,
		)
		return nil
	}

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		slog.Warn(
			"Traceary config is invalid; config-backed features fall back to built-in defaults until the file is fixed: "+configPath,
			"error", err,
		)
		return nil
	}

	return &file
}

// DefaultConfigPath returns the canonical per-user Traceary config path.
func DefaultConfigPath() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", xerrors.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(homeDir, ".config", "traceary", "config.json"), nil
}
