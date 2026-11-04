package runtime

import (
	"context"
	"io"
)

// NativeSessionMetadataSink accepts reduced startup observations for an already
// planned owned child. It never grants recovery, execution or control authority.
// The optional native hook caller must ignore errors and emit no response body.
type NativeSessionMetadataSink interface {
	Observe(context.Context, string, io.Reader) error
}
