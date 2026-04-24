package types

import "context"

// sourceHookContextKey is the unexported key used to thread the
// host-side hook name ("stop", "session_start", "after_agent", ...)
// through the usecase layer so event writers can stamp it on the
// resulting event row without every method signature growing a
// new parameter. Only hook-driven code paths set this; CLI and MCP
// writes leave it unset, which persists as NULL source_hook.
type sourceHookContextKey struct{}

// WithSourceHook returns a derived context that carries the supplied
// hook-event identifier. Use this in hook runtime functions
// immediately before calling into an EventUsecase / SessionUsecase
// so the event written by the usecase picks up the tag.
//
// name should be one of the stable identifiers documented on the
// events.source_hook migration (e.g. "stop", "session_start",
// "subagent_stop", "pre_compact", "after_agent"). Empty names are
// discarded — passing "" is equivalent to not calling WithSourceHook
// at all and keeps the column NULL.
func WithSourceHook(ctx context.Context, name string) context.Context {
	if name == "" {
		return ctx
	}
	return context.WithValue(ctx, sourceHookContextKey{}, name)
}

// SourceHookFromContext extracts the hook-event identifier previously
// stored by WithSourceHook. Returns "" when none is present — the
// caller persists "" as NULL in the database so legacy / non-hook
// writes are distinguishable from hook-driven writes that simply
// forgot to tag.
func SourceHookFromContext(ctx context.Context) string {
	v, _ := ctx.Value(sourceHookContextKey{}).(string)
	return v
}
