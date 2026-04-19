package presentation

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// configFile mirrors the on-disk JSON layout. It is intentionally unexported
// because callers receive the loaded values through Config.
type configFile struct {
	Redact redactSection `json:"redact"`
	Read   readSection   `json:"read"`
}

type redactSection struct {
	ExtraPatterns []string `json:"extra_patterns"`
}

type readSection struct {
	Fields []string `json:"fields"`
}

// Config carries the resolved configuration values consumed by the CLI and
// MCP server. Zero values mean "fall back to the built-in default" so callers
// do not need to distinguish between "file missing" and "key missing".
type Config struct {
	// ExtraRedactPatterns are additional regex patterns applied on top of the
	// built-in audit redaction rules. Nil / empty means "no extras".
	ExtraRedactPatterns []string
	// ReadFields is the default column order applied to tail / list / search
	// text output when the user does not pass --fields. Nil / empty means
	// "fall back to the built-in default column order".
	ReadFields []string
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
		ExtraRedactPatterns: file.Redact.ExtraPatterns,
		ReadFields:          file.Read.Fields,
	}
}

// LoadExtraRedactPatterns preserves the earlier API so callers that only need
// redaction patterns can keep using this single-purpose helper.
func LoadExtraRedactPatterns() []string {
	return LoadConfig().ExtraRedactPatterns
}

func loadConfigFile() *configFile {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Warn(
			"Traceary config path could not be resolved; config-backed features fall back to built-in defaults",
			"error", err,
		)
		return nil
	}

	configPath := filepath.Join(homeDir, ".config", "traceary", "config.json")
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
