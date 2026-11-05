//go:build !darwin && !linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
)

func (LocalOperatorFactory) OpenAutoStart(context.Context, string) (*app.AutoStart, error) {
	return nil, app.ErrRuntimeUnsupported
}
func (LocalOperatorFactory) CreateAutoStart(context.Context, string) (*app.AutoStart, error) {
	return nil, app.ErrRuntimeUnsupported
}
