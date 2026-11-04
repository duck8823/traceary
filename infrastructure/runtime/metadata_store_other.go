//go:build !darwin && !linux

package runtime

import (
	"context"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
)

// MetadataStore refuses platforms without verified private roots/lifetime locks.
type MetadataStore struct{}

func OpenMetadataStore(string, int64) (*MetadataStore, error) { return nil, app.ErrRuntimeUnsupported }
func (*MetadataStore) Export(context.Context) (app.MetadataExport, error) {
	return app.MetadataExport{}, app.ErrRuntimeUnsupported
}
func (*MetadataStore) PreviewForget(context.Context) (app.MetadataForgetPreview, error) {
	return app.MetadataForgetPreview{}, app.ErrRuntimeUnsupported
}
func (*MetadataStore) Forget(context.Context, domain.MetadataForgetConfirmation) (app.MetadataForgetResult, error) {
	return app.MetadataForgetResult{}, app.ErrRuntimeUnsupported
}
