package application

import (
	"context"
	"time"
)

type storeOpenContextKey struct{}
type storeWorkBudgetKey struct{}

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

// WithStoreWorkBudget records the per-batch WallTime so a store method can
// start that timer after ping, not before.
func WithStoreWorkBudget(ctx context.Context, wall time.Duration) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, storeWorkBudgetKey{}, wall)
}

// StoreWorkContext starts a fresh WallTime bound from now, parented on the
// store-open context so a slow ping cannot expire the work deadline.
func StoreWorkContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	wall, ok := ctx.Value(storeWorkBudgetKey{}).(time.Duration)
	if !ok || wall <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(StoreOpenContext(ctx), wall)
}
