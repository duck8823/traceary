package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// ResolveDBPath exposes resolveDBPath for tests.
var ResolveDBPath = resolveDBPath

// HookSessionBoundStateFileName exposes the per-session hook-state file name
// so tests can seed or inspect ended / wake / active-subagent markers.
func HookSessionBoundStateFileName(client string, sessionID types.SessionID) string {
	return hookSessionBoundStateFileName(client, sessionID)
}

// PersistTestHookSpoolRecord writes a replayable spool record for tests.
func PersistTestHookSpoolRecord(command, client, payload string) (string, error) {
	return persistHookSpoolRecord(hookSpoolRecord{
		SchemaVersion: hookSpoolSchemaVersion,
		Command:       command,
		Client:        client,
		Payload:       payload,
		CreatedAt:     time.Now().UTC().Add(-time.Minute),
	})
}

// DrainTestHookSpoolRecords replays pending spool records through the
// production drain path.
func (c *RootCLI) DrainTestHookSpoolRecords(ctx context.Context, limit int) (replayed, failed int) {
	return c.drainHookSpoolRecords(ctx, limit)
}

// ExpectedCodexPluginHookCount exposes the current packaged hook cardinality
// so black-box doctor tests cannot silently drift from the production contract.
var ExpectedCodexPluginHookCount = expectedCodexPluginHookCount

// SetUserHomeDirFunc replaces the home-directory lookup function for tests.
func SetUserHomeDirFunc(f func() (string, error)) {
	storeUserHomeDirFunc(f)
	stateDir, configured := os.LookupEnv(hookStateDirEnvKey)
	if configured && strings.TrimSpace(stateDir) != "" && stateDir != testDefaultHookStateDir {
		return
	}
	homeDir, err := f()
	if err != nil {
		_ = os.Unsetenv(hookStateDirEnvKey)
		return
	}
	_ = os.Setenv(hookStateDirEnvKey, filepath.Join(homeDir, ".config", "traceary", "hooks"))
}

// CallUserHomeDirFunc exposes the current user-home-directory lookup for
// tests. It always reads the active value so overrides installed after
// construction still apply.
func CallUserHomeDirFunc() (string, error) {
	return userHomeDirFunc()
}

// ResetUserHomeDirFunc restores the default home-directory lookup function for tests.
func ResetUserHomeDirFunc() {
	storeUserHomeDirFunc(testDefaultUserHomeDirFunc)
	_ = os.Setenv(hookStateDirEnvKey, testDefaultHookStateDir)
}

// SetAntigravityBundleExistsFunc replaces the Antigravity bundle existence
// probe for tests so the not_installed / installed capability path can be
// exercised deterministically regardless of the host machine.
func SetAntigravityBundleExistsFunc(f func(string) bool) {
	storeAntigravityBundleExistsFunc(f)
}

// ResetAntigravityBundleExistsFunc restores the default Antigravity bundle
// existence probe.
func ResetAntigravityBundleExistsFunc() {
	storeAntigravityBundleExistsFunc(defaultAntigravityBundleExists)
}

// SetGCNowFunc replaces the current-time function for tests.
func SetGCNowFunc(f func() time.Time) {
	storeGCNowFunc(f)
}

// ResetGCNowFunc restores the default current-time function for tests.
func ResetGCNowFunc() {
	storeGCNowFunc(time.Now)
}

// SetTopNowFunc replaces the current-time function used by sessions / top
// snapshot loading for tests.
func SetTopNowFunc(f func() time.Time) {
	storeTopNowFunc(f)
}

// ResetTopNowFunc restores the default top current-time function for tests.
func ResetTopNowFunc() {
	storeTopNowFunc(time.Now)
}

// SetAntigravityPendingNowFunc replaces the current-time function used for
// Antigravity pending-state TTL pruning for tests.
func SetAntigravityPendingNowFunc(f func() time.Time) {
	storeAntigravityPendingNowFunc(f)
}

// ResetAntigravityPendingNowFunc restores the default current-time function
// used for Antigravity pending-state TTL pruning.
func ResetAntigravityPendingNowFunc() {
	storeAntigravityPendingNowFunc(time.Now)
}

// SetAntigravityProcessCwdFunc replaces the process-cwd lookup used when
// Antigravity payloads omit workspacePaths.
func SetAntigravityProcessCwdFunc(f func(int) (string, error)) {
	storeAntigravityProcessCwdFunc(f)
}

// ResetAntigravityProcessCwdFunc restores the default process-cwd lookup.
func ResetAntigravityProcessCwdFunc() {
	storeAntigravityProcessCwdFunc(defaultAntigravityProcessCwd)
}

// SetAntigravityParentPIDFunc replaces the parent-PID seed used for workspace
// fallback discovery.
func SetAntigravityParentPIDFunc(f func() int) {
	storeAntigravityParentPIDFunc(f)
}

// ResetAntigravityParentPIDFunc restores the default parent-PID seed.
func ResetAntigravityParentPIDFunc() {
	storeAntigravityParentPIDFunc(os.Getppid)
}

// AntigravityWorkspaceCwd exposes antigravityWorkspaceCwd for tests.
func AntigravityWorkspaceCwd(payload []byte) string {
	return antigravityWorkspaceCwd(payload)
}

// AntigravityPendingCommandPath exposes the resolved pending-state file path for
// a conversation/step pair so tests can age or inspect it directly.
func AntigravityPendingCommandPath(conversationID, stepIdx string) (string, error) {
	return antigravityPendingCommandPath(conversationID, stepIdx)
}

// SetResolveHookTranscriptSessionIDFunc overrides the session-ID resolver
// runHookTranscriptWithBlocks uses. Tests use it to force the "session
// resolution yielded nothing" fail-soft skip (recorded=false, err=nil) in
// isolation from any particular caller's own upstream preconditions — e.g.
// Kimi's idempotency guard (#1681), whose own turn-resolution check would
// otherwise make that skip unreachable through payload manipulation alone.
func SetResolveHookTranscriptSessionIDFunc(f func([]byte, string) (types.SessionID, error)) {
	storeResolveHookTranscriptSessionIDFunc(f)
}

// ResetResolveHookTranscriptSessionIDFunc restores the default session-ID
// resolver for runHookTranscriptWithBlocks.
func ResetResolveHookTranscriptSessionIDFunc() {
	storeResolveHookTranscriptSessionIDFunc(resolveHookSessionID)
}

// SetAfterInspectGrokTranscriptHook runs fn after inspectGrokTranscript has
// classified the wire log. Tests use it to remove or rewrite updates.jsonl
// so a second extract would fail-soft (#1713).
func SetAfterInspectGrokTranscriptHook(fn func()) {
	storeAfterInspectGrokTranscriptHook(fn)
}

// ResetAfterInspectGrokTranscriptHook clears the post-inspect test hook.
func ResetAfterInspectGrokTranscriptHook() {
	storeAfterInspectGrokTranscriptHook(nil)
}

// SetDetectRepoContextFunc replaces the work-context resolver for tests.
func SetDetectRepoContextFunc(f func(context.Context) (string, error)) {
	storeDetectRepoContextFunc(f)
}

// ResetDetectRepoContextFunc restores the default work-context resolver for tests.
func ResetDetectRepoContextFunc() {
	storeDetectRepoContextFunc(detectRepoContext)
}
