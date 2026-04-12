package presentation

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
)

// configFile mirrors the on-disk JSON layout. It is intentionally unexported
// because callers receive the loaded values directly.
type configFile struct {
	Redact redactSection `json:"redact"`
}

type redactSection struct {
	ExtraPatterns []string `json:"extra_patterns"`
}

// LoadExtraRedactPatterns reads the optional Traceary config and returns the
// extra redaction patterns. When the config cannot be used the returned slice
// is nil and a warning is logged via slog.
func LoadExtraRedactPatterns() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Warn(
			"Traceary config path could not be resolved; extra audit redaction patterns are disabled",
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
			"Traceary config could not be read; extra audit redaction patterns are disabled until the file is readable: "+configPath,
			"error", err,
		)
		return nil
	}

	var file configFile
	if err := json.Unmarshal(data, &file); err != nil {
		slog.Warn(
			"Traceary config is invalid; extra audit redaction patterns are disabled until the file is fixed: "+configPath,
			"error", err,
		)
		return nil
	}

	return file.Redact.ExtraPatterns
}
