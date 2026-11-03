package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
)

func (localAutoStartDescriptor) RenderAutoStartDescriptor(context.Context) (string, error) {
	return "", app.ErrRuntimeUnsupported
}
