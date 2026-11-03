//go:build darwin || linux

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"

	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
)

const maxMetadataExportBytes = 16 * 1024 * 1024

// MetadataStore audits existing metadata; it never opens core SQL or native hosts.
// The write capability remains private and uses the existing atomic envelope writer.
type MetadataStore struct {
	execution *ExecutionStore
	commit    func(context.Context, int, string, []byte, *unix.Stat_t, func() error) error
}

// OpenMetadataStore opens an existing private root; no missing directory is created.
func OpenMetadataStore(root string, budget int64) (*MetadataStore, error) {
	execution, err := OpenExecutionStore(root, budget)
	if err != nil {
		return nil, err
	}
	return &MetadataStore{execution: execution, commit: execution.metadata.writeMetadataChecked}, nil
}

type metadataAuditState struct {
	export         app.MetadataExport
	execution      executionEnvelope
	executionStamp *unix.Stat_t
	controls       controlEnvelope
	controlStamp   *unix.Stat_t
}

// Export returns only bounded safe summaries, not the raw storage envelopes.
func (s *MetadataStore) Export(ctx context.Context) (app.MetadataExport, error) {
	var result app.MetadataExport
	err := s.execution.metadata.locked(ctx, func(fd int) error {
		state, err := s.readAudit(ctx, fd)
		if err != nil {
			return err
		}
		result = state.export
		return nil
	})
	if err != nil {
		return app.MetadataExport{}, xerrors.Errorf("export runtime metadata: %w", err)
	}
	return result, nil
}

// PreviewForget never changes history or starts/stops a daemon. Coordination lock
// files may be created in an existing root, as for other read-side store operations.
func (s *MetadataStore) PreviewForget(ctx context.Context) (app.MetadataForgetPreview, error) {
	snapshot, err := s.Export(ctx)
	if err != nil {
		return app.MetadataForgetPreview{}, err
	}
	return app.MetadataForgetPreview{Snapshot: snapshot, RecoveryRecordLoss: true, OfflineRequired: true}, nil
}

func metadataStampEqual(a, b *unix.Stat_t) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Dev == b.Dev && a.Ino == b.Ino && a.Size == b.Size && a.Mode == b.Mode && a.Uid == b.Uid && a.Nlink == b.Nlink && a.Ctim == b.Ctim && a.Mtim == b.Mtim
}

func (s *MetadataStore) readAudit(ctx context.Context, fd int) (metadataAuditState, error) {
	if err := ctx.Err(); err != nil {
		return metadataAuditState{}, xerrors.Errorf("read runtime metadata canceled: %w", err)
	}
	if err := auditMetadataInventory(fd); err != nil {
		return metadataAuditState{}, err
	}
	m := s.execution.metadata
	registry, registryStamp, err := m.read(fd)
	if err != nil {
		return metadataAuditState{}, err
	}
	execution, executionStamp, err := s.execution.read(fd)
	if err != nil {
		return metadataAuditState{}, err
	}
	controls, controlStamp, _, err := s.execution.readControls(fd)
	if err != nil {
		return metadataAuditState{}, err
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "traceary-runtime-metadata-v1\x00%d:%s\x00", len(m.witness), m.witness)
	for _, file := range []struct {
		name  string
		limit int64
		stamp *unix.Stat_t
	}{
		{"registry.json", m.budget, registryStamp}, {"execution.json", min(maxExecutionBytes, m.budget), executionStamp}, {"receipts.json", min(maxControlMetadataBytes, m.budget), controlStamp},
	} {
		if err = ctx.Err(); err != nil {
			return metadataAuditState{}, xerrors.Errorf("hash runtime metadata canceled: %w", err)
		}
		data, stamp, e := m.readMetadata(fd, file.name, file.limit)
		if e != nil {
			return metadataAuditState{}, e
		}
		if !metadataStampEqual(file.stamp, stamp) {
			return metadataAuditState{}, domain.ErrChanged
		}
		_, _ = fmt.Fprintf(hash, "%s:%t:%d:", file.name, stamp != nil, len(data))
		_, _ = hash.Write(data)
	}
	view := app.MetadataExport{Version: 1, Authority: "persisted_metadata_not_liveness", RootWitness: m.witness, Digest: hex.EncodeToString(hash.Sum(nil)), RegistryRevision: strconv.FormatUint(registry.Snapshot().Revision, 10), ExecutionRevision: strconv.FormatUint(*execution.Revision, 10), ReceiptRevision: strconv.FormatUint(*controls.Revision, 10), Profiles: make([]app.MetadataProfileSummary, 0, len(execution.Profiles)), Runs: make([]app.MetadataRunSummary, 0, len(execution.Launches))}
	counts := make(map[string]int, len(controls.Runs))
	uncertainReceipts := make(map[string]int, len(controls.Runs))
	for _, group := range controls.Runs {
		counts[group.Selection.Run] = len(group.Receipts)
		view.TotalReceipts += len(group.Receipts)
		for _, receipt := range group.Receipts {
			if receipt.Outcome == string(domain.ControlPending) || receipt.Outcome == string(domain.ControlUnknown) {
				uncertainReceipts[group.Selection.Run]++
				view.UncertainReceipts++
			}
		}
	}
	for _, profile := range execution.Profiles {
		view.Profiles = append(view.Profiles, app.MetadataProfileSummary{ID: domain.ProfileID(profile.ID), Host: profile.Host, Version: profile.Version})
	}
	for _, run := range execution.Launches {
		uncertain := run.State != string(domain.RunExited) || *run.Uncertain || uncertainReceipts[run.Identity.Run] > 0
		if uncertain {
			view.UncertainRuns++
		}
		view.Runs = append(view.Runs, app.MetadataRunSummary{Run: run.Identity.Run, Generation: run.Identity.Generation, Epoch: run.Identity.Epoch, Worktree: domain.WorktreeID(run.Worktree.ID), Profile: domain.ProfileID(run.Profile.ID), State: domain.RunState(run.State), Uncertain: uncertain, Receipts: counts[run.Identity.Run], UncertainReceipts: uncertainReceipts[run.Identity.Run]})
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		return metadataAuditState{}, domain.ErrInvalid
	}
	if len(encoded) > maxMetadataExportBytes {
		return metadataAuditState{}, domain.ErrLimit
	}
	if err = ctx.Err(); err != nil {
		return metadataAuditState{}, xerrors.Errorf("finish runtime metadata audit canceled: %w", err)
	}
	if err = m.verifyRoot(fd); err != nil {
		return metadataAuditState{}, err
	}
	return metadataAuditState{view, execution, executionStamp, controls, controlStamp}, nil
}

func auditMetadataInventory(fd int) error {
	copyFD, err := unix.Dup(fd)
	if err != nil {
		return domain.ErrUnsafe
	}
	file := os.NewFile(uintptr(copyFD), "runtime metadata inventory")
	defer func() { _ = file.Close() }()
	entries, err := file.ReadDir(17)
	if err != nil && !errors.Is(err, io.EOF) {
		return domain.ErrUnsafe
	}
	if len(entries) > 16 {
		return domain.ErrLimit
	}
	for _, entry := range entries {
		name := entry.Name()
		switch name {
		case "registry.json", "execution.json", "receipts.json", "daemon.lock", "registry.lock", "daemon.sock", "observations.sock":
		default:
			// Known later lifecycle/recovery formats require their actual codecs
			// and erasure/retention contract. Never silently omit or unlink them.
			return domain.ErrUnsupported
		}
		var stamp unix.Stat_t
		if unix.Fstatat(fd, name, &stamp, unix.AT_SYMLINK_NOFOLLOW) != nil {
			return domain.ErrUnsafe
		}
		if name == "daemon.sock" || name == "observations.sock" {
			if !runtimeSocketStat(stamp) {
				return domain.ErrUnsafe
			}
		} else if !privateRegular(stamp) {
			return domain.ErrUnsafe
		}
	}
	return nil
}

// metadataOfflineLease has actual lifetime ownership, not a saved PID or a clean
// flag. Same-process claim precedes the OS lease because Darwin flock is not a
// sufficient independent-descriptor same-process exclusion mechanism.
func (s *MetadataStore) offlineLease(ctx context.Context, fn func(func() error) error) error {
	if err := ctx.Err(); err != nil {
		return xerrors.Errorf("acquire offline metadata lease canceled: %w", err)
	}
	m := s.execution.metadata
	fd, err := privateRuntimeDir(m.root, false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(fd) }()
	if err = m.verifyRoot(fd); err != nil {
		return err
	}
	release, err := claimDaemonRoot(m.witness)
	if err != nil {
		return err
	}
	defer release()
	lock, err := openPrivateLock(fd, "daemon.lock")
	if err != nil {
		return domain.ErrUnsafe
	}
	defer func() { _ = unix.Close(lock) }()
	var original unix.Stat_t
	if unix.Fstat(lock, &original) != nil || !privateRegular(original) {
		return domain.ErrUnsafe
	}
	if err = unix.Flock(lock, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return app.ErrRuntimeBusy
		}
		return domain.ErrUnsafe
	}
	defer func() { _ = unix.Flock(lock, unix.LOCK_UN) }()
	current := func() error {
		if e := m.verifyRoot(fd); e != nil {
			return e
		}
		var now unix.Stat_t
		if unix.Fstatat(fd, "daemon.lock", &now, unix.AT_SYMLINK_NOFOLLOW) != nil || !privateRegular(now) || now.Dev != original.Dev || now.Ino != original.Ino {
			return domain.ErrChanged
		}
		return nil
	}
	if err = current(); err != nil {
		return err
	}
	return fn(current)
}

type metadataReplacement struct {
	name  string
	data  []byte
	stamp *unix.Stat_t
}

// forgetWrites owns the cross-envelope integrity rule: all increments are
// reserved before any effect, receipt groups disappear before their launches,
// profiles survive, and alreadyempty envelopes are never rewritten.
func (state metadataAuditState) forgetWrites() ([]metadataReplacement, error) {
	if len(state.controls.Runs) > 0 && *state.controls.Revision == math.MaxUint64 || len(state.execution.Launches) > 0 && *state.execution.Revision == math.MaxUint64 {
		return nil, domain.ErrLimit
	}
	writes := make([]metadataReplacement, 0, 2)
	if len(state.controls.Runs) > 0 {
		revision := *state.controls.Revision + 1
		doc := controlEnvelope{Version: state.controls.Version, Revision: &revision, Runs: []controlRunRecord{}}
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, domain.ErrInvalid
		}
		writes = append(writes, metadataReplacement{"receipts.json", data, state.controlStamp})
	}
	if len(state.execution.Launches) > 0 {
		revision := *state.execution.Revision + 1
		doc := state.execution
		doc.Revision = &revision
		doc.Launches = []launchRecord{}
		data, err := json.Marshal(doc)
		if err != nil {
			return nil, domain.ErrInvalid
		}
		writes = append(writes, metadataReplacement{"execution.json", data, state.executionStamp})
	}
	return writes, nil
}

// Forget requires an exact preview and actual offline lease. It never signals a
// saved PID, claims native death, deletes native conversations or retries writes.
func (s *MetadataStore) Forget(ctx context.Context, confirmation domain.MetadataForgetConfirmation) (app.MetadataForgetResult, error) {
	if !confirmation.Valid() {
		return app.MetadataForgetResult{}, domain.ErrInvalid
	}
	var result app.MetadataForgetResult
	err := s.offlineLease(ctx, func(leaseCurrent func() error) error {
		return s.execution.metadata.locked(ctx, func(fd int) error {
			if err := leaseCurrent(); err != nil {
				return err
			}
			state, err := s.readAudit(ctx, fd)
			if err != nil {
				return err
			}
			if state.export.Digest != confirmation.Digest() {
				return domain.ErrChanged
			}
			writes, err := state.forgetWrites()
			if err != nil {
				return err
			}
			for _, write := range writes {
				if err = s.execution.metadata.metadataBudget(fd, write.name, int64(len(write.data))); err != nil {
					return err
				}
			}
			committed := false
			for _, write := range writes {
				if err = ctx.Err(); err == nil {
					err = leaseCurrent()
				}
				if err == nil {
					err = s.commit(ctx, fd, write.name, write.data, write.stamp, leaseCurrent)
				}
				if err != nil {
					if committed || errors.Is(err, ErrCommitUncertain) {
						return errors.Join(app.ErrMetadataForgetUncertain, err)
					}
					return err
				}
				committed = true
			}
			if err = leaseCurrent(); err != nil {
				if committed {
					return errors.Join(app.ErrMetadataForgetUncertain, err)
				}
				return err
			}
			outcome := "unchanged"
			if committed {
				outcome = "forgotten"
			}
			result = app.MetadataForgetResult{Outcome: outcome, Runs: len(state.export.Runs), Receipts: state.export.TotalReceipts}
			return nil
		})
	})
	if err != nil {
		return app.MetadataForgetResult{}, xerrors.Errorf("forget runtime metadata: %w", err)
	}
	return result, nil
}
