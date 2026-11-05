//go:build !darwin && !linux

// Keep this isolated adapter contract in-package so explicit-file tests can
// execute the unsupported stub on a supported host without selecting its sink.
package runtime

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	app "github.com/duck8823/traceary/application/runtime"
)

type unsupportedAGYInput struct{ reads int }

func (r *unsupportedAGYInput) Read([]byte) (int, error) {
	r.reads++
	return 0, io.EOF
}

func TestUnsupportedAntigravityMetadataRefusesWithoutReadingOrCreatingFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "must-not-exist")
	input := &unsupportedAGYInput{}
	sink := AntigravityStatuslineSink{}
	if err := sink.Observe(context.Background(), root, input); !errors.Is(err, app.ErrRuntimeUnsupported) {
		t.Fatalf("expected unsupported platform, got %v", err)
	}
	if input.reads != 0 {
		t.Fatalf("unsupported metadata consumed input %d times", input.reads)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsupported metadata touched its target: %v", err)
	}
	if err := sink.Observe(context.Background(), root, nil); !errors.Is(err, app.ErrRuntimeUnsupported) {
		t.Fatalf("nil input must remain unsupported, got %v", err)
	}
}
