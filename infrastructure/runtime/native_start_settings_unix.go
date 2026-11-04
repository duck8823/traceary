//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	domain "github.com/duck8823/traceary/domain/runtime"
)

type nativeMetadataCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}
type nativeMetadataMatcher struct {
	Hooks []nativeMetadataCommand `json:"hooks"`
}
type nativeMetadataSettings struct {
	Hooks struct {
		SessionStart []nativeMetadataMatcher `json:"SessionStart"`
	} `json:"hooks"`
}

// nativeShellLiteral is only for internally planned executable/root literals.
// Native stdin is never interpolated into a shell command.
func nativeShellLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

// writeSettings adds one distinct metadata-only SessionStart handler, not a
// duplicate primary recorder or an override of native auth/permission settings.
// Only the owner can prepare it; public observation calls cannot choose argv.
func (s *nativeStartSlot) writeSettings(ctx context.Context, executable string) (string, error) {
	if !canonicalNativePath(executable) {
		return "", domain.ErrInvalid
	}
	settings := nativeMetadataSettings{}
	command := nativeShellLiteral(executable) + " runtime native-session-start --slot " + nativeShellLiteral(s.metadata.root) + " >/dev/null 2>&1 || :"
	settings.Hooks.SessionStart = []nativeMetadataMatcher{{Hooks: []nativeMetadataCommand{{Type: "command", Command: command, Timeout: 6}}}}
	data, err := json.Marshal(settings)
	if err != nil {
		return "", domain.ErrInvalid
	}
	if int64(len(data)) > maxNativeMarkerBytes {
		return "", domain.ErrLimit
	}
	err = s.metadata.locked(ctx, func(fd int) error {
		intent, err := readNativeIntent(s.metadata, fd)
		if err != nil {
			return err
		}
		if intent != s.intent {
			return domain.ErrChanged
		}
		old, stamp, err := s.metadata.readMetadata(fd, "settings.json", maxNativeMarkerBytes)
		if err != nil {
			return err
		}
		if stamp != nil {
			if !bytes.Equal(old, data) {
				return domain.ErrChanged
			}
			return s.metadata.verifyRoot(fd)
		}
		return s.metadata.writeMetadata(ctx, fd, "settings.json", data, nil)
	})
	if err != nil {
		return "", err
	}
	return filepath.Join(s.metadata.root, "settings.json"), nil
}
