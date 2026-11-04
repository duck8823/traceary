//go:build !darwin && !linux

package runtime

import (
	"context"
	"io"

	app "github.com/duck8823/traceary/application/runtime"
)

// NativeSessionMarkerSink refuses platforms without verified private metadata.
type NativeSessionMarkerSink struct{}

func (NativeSessionMarkerSink) Observe(context.Context, string, io.Reader) error {
	return app.ErrRuntimeUnsupported
}
