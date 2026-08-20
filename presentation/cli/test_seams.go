package cli

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/duck8823/traceary/domain/types"
)

// Test-only seams live in package-level slots so a serial test can replace
// them while a parallel test still reads them. Production never writes.
// Each wrapper loads the current pointer; Set* / store* publish a new one.

type (
	userHomeDirFn                    = func() (string, error)
	antigravityBundleExistsFn        = func(string) bool
	nowFn                            = func() time.Time
	antigravityProcessCwdFn          = func(int) (string, error)
	antigravityParentPIDFn           = func() int
	resolveHookTranscriptSessionIDFn = func([]byte, string) (types.SessionID, error)
	afterInspectGrokTranscriptHookFn = func()
	detectRepoContextFn              = func(context.Context) (string, error)
	listTracearyProcessSnapshotsFn   = func() ([]tracearyProcessSnapshot, error)
)

var (
	userHomeDirSlot                    atomic.Pointer[userHomeDirFn]
	antigravityBundleExistsSlot        atomic.Pointer[antigravityBundleExistsFn]
	gcNowSlot                          atomic.Pointer[nowFn]
	topNowSlot                         atomic.Pointer[nowFn]
	antigravityPendingNowSlot          atomic.Pointer[nowFn]
	antigravityProcessCwdSlot          atomic.Pointer[antigravityProcessCwdFn]
	antigravityParentPIDSlot           atomic.Pointer[antigravityParentPIDFn]
	resolveHookTranscriptSessionIDSlot atomic.Pointer[resolveHookTranscriptSessionIDFn]
	afterInspectGrokTranscriptHookSlot atomic.Pointer[afterInspectGrokTranscriptHookFn]
	detectRepoContextSlot              atomic.Pointer[detectRepoContextFn]
	listTracearyProcessSnapshotsSlot   atomic.Pointer[listTracearyProcessSnapshotsFn]
)

func init() {
	storeUserHomeDirFunc(os.UserHomeDir)
	storeAntigravityBundleExistsFunc(defaultAntigravityBundleExists)
	storeGCNowFunc(time.Now)
	storeTopNowFunc(time.Now)
	storeAntigravityPendingNowFunc(time.Now)
	storeAntigravityProcessCwdFunc(defaultAntigravityProcessCwd)
	storeAntigravityParentPIDFunc(os.Getppid)
	storeResolveHookTranscriptSessionIDFunc(resolveHookSessionID)
	storeDetectRepoContextFunc(detectRepoContext)
	storeListTracearyProcessSnapshotsFunc(defaultListTracearyProcessSnapshots)
}

func storeUserHomeDirFunc(f userHomeDirFn) {
	userHomeDirSlot.Store(&f)
}

func userHomeDirFunc() (string, error) {
	return (*userHomeDirSlot.Load())()
}

func currentUserHomeDirFunc() userHomeDirFn {
	return *userHomeDirSlot.Load()
}

func storeAntigravityBundleExistsFunc(f antigravityBundleExistsFn) {
	antigravityBundleExistsSlot.Store(&f)
}

func antigravityBundleExistsFunc(path string) bool {
	return (*antigravityBundleExistsSlot.Load())(path)
}

func storeGCNowFunc(f nowFn) {
	gcNowSlot.Store(&f)
}

func gcNowFunc() time.Time {
	return (*gcNowSlot.Load())()
}

func storeTopNowFunc(f nowFn) {
	topNowSlot.Store(&f)
}

func topNowFunc() time.Time {
	return (*topNowSlot.Load())()
}

func currentTopNowFunc() nowFn {
	return *topNowSlot.Load()
}

func storeAntigravityPendingNowFunc(f nowFn) {
	antigravityPendingNowSlot.Store(&f)
}

func antigravityPendingNowFunc() time.Time {
	return (*antigravityPendingNowSlot.Load())()
}

func storeAntigravityProcessCwdFunc(f antigravityProcessCwdFn) {
	antigravityProcessCwdSlot.Store(&f)
}

func antigravityProcessCwdFunc(pid int) (string, error) {
	return (*antigravityProcessCwdSlot.Load())(pid)
}

func storeAntigravityParentPIDFunc(f antigravityParentPIDFn) {
	antigravityParentPIDSlot.Store(&f)
}

func antigravityParentPIDFunc() int {
	return (*antigravityParentPIDSlot.Load())()
}

func storeResolveHookTranscriptSessionIDFunc(f resolveHookTranscriptSessionIDFn) {
	resolveHookTranscriptSessionIDSlot.Store(&f)
}

func resolveHookTranscriptSessionIDFunc(payload []byte, client string) (types.SessionID, error) {
	return (*resolveHookTranscriptSessionIDSlot.Load())(payload, client)
}

func storeAfterInspectGrokTranscriptHook(fn afterInspectGrokTranscriptHookFn) {
	if fn == nil {
		afterInspectGrokTranscriptHookSlot.Store(nil)
		return
	}
	afterInspectGrokTranscriptHookSlot.Store(&fn)
}

func invokeAfterInspectGrokTranscriptHook() {
	p := afterInspectGrokTranscriptHookSlot.Load()
	if p == nil || *p == nil {
		return
	}
	(*p)()
}

func storeDetectRepoContextFunc(f detectRepoContextFn) {
	detectRepoContextSlot.Store(&f)
}

func detectRepoContextFunc(ctx context.Context) (string, error) {
	return (*detectRepoContextSlot.Load())(ctx)
}

func storeListTracearyProcessSnapshotsFunc(f listTracearyProcessSnapshotsFn) {
	listTracearyProcessSnapshotsSlot.Store(&f)
}

func listTracearyProcessSnapshotsFunc() ([]tracearyProcessSnapshot, error) {
	return (*listTracearyProcessSnapshotsSlot.Load())()
}
