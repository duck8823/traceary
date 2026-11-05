//go:build !darwin && !linux

package runtime

import (
	"context"
	"io"

	app "github.com/duck8823/traceary/application/runtime"
)

// AntigravityStatuslineSink refuses platforms without verified private metadata.
// It must not consume a callback or attempt filesystem preparation.
type AntigravityStatuslineSink struct{}

var _ app.NativeSessionMetadataSink = AntigravityStatuslineSink{}

func (AntigravityStatuslineSink) Observe(context.Context, string, io.Reader) error {
	return app.ErrRuntimeUnsupported
}
