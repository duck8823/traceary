package application

import "context"

type storeOpenContextKey struct{}

// WithStoreOpenContext attaches openCtx as the context used to open and ping
// a store connection, while workCtx continues to bound batch work. Resume
// uses this so the per-batch WallTime does not include store-open cost.
func WithStoreOpenContext(workCtx, openCtx context.Context) context.Context {
	if workCtx == nil {
		return openCtx
	}
	if openCtx == nil {
		return workCtx
	}
	return context.WithValue(workCtx, storeOpenContextKey{}, openCtx)
}

// StoreOpenContext returns the context that should bound connection open and
// ping. When WithStoreOpenContext was not used, it returns ctx unchanged.
func StoreOpenContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	openCtx, ok := ctx.Value(storeOpenContextKey{}).(context.Context)
	if !ok || openCtx == nil {
		return ctx
	}
	return openCtx
}
