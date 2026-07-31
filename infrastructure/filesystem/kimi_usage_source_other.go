//go:build !unix

package filesystem

import (
	"context"

	"golang.org/x/xerrors"

	"github.com/duck8823/traceary/application"
)

type kimiUsageSource struct{}

// NewKimiUsageSource creates the contained, body-free Kimi wire adapter.
//
// Platform-support policy: the source is supported on Unix platforms (macOS
// and Linux), where Kimi Code writes session_index.jsonl and per-session
// agents/main/wire.jsonl under the Kimi config home (KIMI_CODE_HOME or
// ~/.kimi-code). This platform is unsupported, so the adapter fails closed:
// Load always returns an explicit error and never fabricates usage data.
func NewKimiUsageSource() application.KimiUsageSource {
	return &kimiUsageSource{}
}

func (s *kimiUsageSource) Load(context.Context, string) (application.KimiUsageLoadResult, error) {
	return application.KimiUsageLoadResult{}, xerrors.New("Kimi usage source requires Unix contained file-open primitives")
}
