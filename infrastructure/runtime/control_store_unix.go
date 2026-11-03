//go:build darwin || linux

package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/sys/unix"
	"golang.org/x/xerrors"
	"io"
	"math"
	"unicode/utf8"
)

const maxControlMetadataBytes int64 = 64 * 1024 * 1024

type controlRunRecord struct {
	Selection outputSelectionRecord  `json:"selection"`
	Receipts  []controlReceiptRecord `json:"receipts"`
}
type controlEnvelope struct {
	Version  int                `json:"version"`
	Revision *uint64            `json:"revision"`
	Runs     []controlRunRecord `json:"runs"`
}

func decodeControlMetadata(data []byte, target any) error {
	if !utf8.Valid(data) {
		return domain.ErrInvalid
	}
	if err := uniqueJSONValue(json.NewDecoder(bytes.NewReader(data)), 0); err != nil {
		return err
	}
	tokens := json.NewDecoder(bytes.NewReader(data))
	for {
		token, err := tokens.Token()
		if err == io.EOF {
			break
		}
		if err != nil || token == nil {
			return domain.ErrInvalid
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		return domain.ErrInvalid
	}
	return nil
}
func (s *ExecutionStore) readControls(fd int) (controlEnvelope, *unix.Stat_t, map[string]app.OutputSelection, error) {
	data, stamp, err := s.metadata.readMetadata(fd, "receipts.json", min(maxControlMetadataBytes, s.metadata.budget))
	if err != nil {
		return controlEnvelope{}, nil, nil, err
	}
	revision := uint64(0)
	doc := controlEnvelope{Version: 1, Revision: &revision, Runs: []controlRunRecord{}}
	if stamp != nil {
		if err = decodeControlMetadata(data, &doc); err != nil {
			return controlEnvelope{}, nil, nil, err
		}
		if doc.Version != 1 {
			return controlEnvelope{}, nil, nil, domain.ErrUnsupported
		}
		if doc.Revision == nil || doc.Runs == nil || len(doc.Runs) > domain.MaxLaunchRequests {
			return controlEnvelope{}, nil, nil, domain.ErrInvalid
		}
	}
	executions, _, err := s.read(fd)
	if err != nil {
		return controlEnvelope{}, nil, nil, err
	}
	bindings := make(map[string]app.OutputSelection, len(executions.Launches))
	for _, record := range executions.Launches {
		snapshot, e := record.domainSnapshot()
		if e != nil {
			return controlEnvelope{}, nil, nil, e
		}
		bindings[snapshot.Identity.Run] = app.OutputSelection{Identity: snapshot.Identity, Worktree: snapshot.Target.Worktree().ID(), Profile: snapshot.Profile.ID()}
	}
	seen := make(map[string]bool, len(doc.Runs))
	total := 0
	for _, group := range doc.Runs {
		selection, e := group.Selection.selection()
		if e != nil {
			return controlEnvelope{}, nil, nil, domain.ErrInvalid
		}
		if seen[selection.Identity.Run] || bindings[selection.Identity.Run] != selection || group.Receipts == nil || len(group.Receipts) == 0 || len(group.Receipts) > domain.MaxControlReceipts {
			return controlEnvelope{}, nil, nil, domain.ErrInvalid
		}
		seen[selection.Identity.Run] = true
		ids := make(map[string]bool, len(group.Receipts))
		nonLifecycle := 0
		for _, record := range group.Receipts {
			receipt, e := record.receipt(selection)
			if e != nil {
				return controlEnvelope{}, nil, nil, e
			}
			snapshot := receipt.Snapshot()
			if ids[snapshot.Intent.RequestID] {
				return controlEnvelope{}, nil, nil, domain.ErrInvalid
			}
			ids[snapshot.Intent.RequestID] = true
			if snapshot.Intent.Kind != domain.ControlStop {
				nonLifecycle++
			}
		}
		if nonLifecycle > domain.MaxControlReceipts-domain.ReservedLifecycleReceipts {
			return controlEnvelope{}, nil, nil, domain.ErrLimit
		}
		total += len(group.Receipts)
	}
	if *doc.Revision < uint64(total) {
		return controlEnvelope{}, nil, nil, domain.ErrInvalid
	}
	if stamp != nil {
		if err = s.metadata.metadataBudget(fd, "receipts.json", int64(len(data))); err != nil {
			return controlEnvelope{}, nil, nil, err
		}
	}
	return doc, stamp, bindings, nil
}
func (s *ExecutionStore) updateControl(ctx context.Context, selection app.OutputSelection, receipt domain.ControlReceipt, reserve bool) error {
	if _, err := recordSelection(selection).selection(); err != nil {
		return domain.ErrInvalid
	}
	snapshot := receipt.Snapshot()
	if snapshot.Identity != selection.Identity {
		return domain.ErrChanged
	}
	if _, err := domain.ControlReceiptFrom(snapshot); err != nil {
		return domain.ErrInvalid
	}
	if reserve && snapshot.Outcome != domain.ControlPending || !reserve && snapshot.Outcome == domain.ControlPending {
		return domain.ErrInvalid
	}
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, stamp, bindings, err := s.readControls(fd)
		if err != nil {
			return err
		}
		if bindings[selection.Identity.Run] != selection {
			return domain.ErrChanged
		}
		if *doc.Revision == math.MaxUint64 {
			return domain.ErrLimit
		}
		index := -1
		for i, group := range doc.Runs {
			if group.Selection.Run == selection.Identity.Run {
				index = i
				break
			}
		}
		if index < 0 {
			if !reserve {
				return domain.ErrNotFound
			}
			if len(doc.Runs) >= domain.MaxLaunchRequests {
				return domain.ErrLimit
			}
			doc.Runs = append(doc.Runs, controlRunRecord{Selection: recordSelection(selection), Receipts: []controlReceiptRecord{}})
			index = len(doc.Runs) - 1
		}
		group := &doc.Runs[index]
		found := -1
		nonLifecycle := 0
		for i, record := range group.Receipts {
			if record.ID == snapshot.Intent.RequestID {
				found = i
			}
			if domain.ControlKind(record.Kind) != domain.ControlStop {
				nonLifecycle++
			}
		}
		if reserve {
			if found >= 0 {
				old, e := group.Receipts[found].receipt(selection)
				if e != nil {
					return e
				}
				if old.Snapshot().Intent != snapshot.Intent {
					return domain.ErrChanged
				}
				return domain.ErrDuplicate
			}
			if len(group.Receipts) >= domain.MaxControlReceipts || snapshot.Intent.Kind != domain.ControlStop && nonLifecycle >= domain.MaxControlReceipts-domain.ReservedLifecycleReceipts {
				return domain.ErrLimit
			}
			group.Receipts = append(group.Receipts, recordControl(receipt))
		} else {
			if found < 0 {
				return domain.ErrNotFound
			}
			old, e := group.Receipts[found].receipt(selection)
			if e != nil {
				return e
			}
			previous := old.Snapshot()
			if previous.Intent != snapshot.Intent {
				return domain.ErrChanged
			}
			if previous.Outcome != domain.ControlPending {
				if previous == snapshot {
					return nil
				}
				return domain.ErrChanged
			}
			group.Receipts[found] = recordControl(receipt)
		}
		*doc.Revision++
		data, err := json.Marshal(doc)
		if err != nil {
			return domain.ErrInvalid
		}
		if int64(len(data)) > maxControlMetadataBytes {
			return domain.ErrLimit
		}
		return s.metadata.writeMetadata(ctx, fd, "receipts.json", data, stamp)
	})
	if errors.Is(err, ErrCommitUncertain) {
		return errors.Join(app.ErrControlCommitUncertain, err)
	}
	return err
}

// ReserveControl commits safe intent before any effect; ambiguous commits have a portable marker.
func (s *ExecutionStore) ReserveControl(ctx context.Context, selection app.OutputSelection, receipt domain.ControlReceipt) error {
	if err := s.updateControl(ctx, selection, receipt, true); err != nil {
		return xerrors.Errorf("reserve runtime control metadata: %w", err)
	}
	return nil
}

// SaveControl completes an existing receipt and refuses rewriting terminal evidence.
func (s *ExecutionStore) SaveControl(ctx context.Context, selection app.OutputSelection, receipt domain.ControlReceipt) error {
	if err := s.updateControl(ctx, selection, receipt, false); err != nil {
		return xerrors.Errorf("complete runtime control metadata: %w", err)
	}
	return nil
}

// ListControlReceipts reads safe historical metadata without creating live child authority.
func (s *ExecutionStore) ListControlReceipts(ctx context.Context, selection app.OutputSelection) ([]domain.ControlReceipt, error) {
	if _, err := recordSelection(selection).selection(); err != nil {
		return nil, domain.ErrInvalid
	}
	result := []domain.ControlReceipt{}
	err := s.metadata.locked(ctx, func(fd int) error {
		doc, _, bindings, err := s.readControls(fd)
		if err != nil {
			return err
		}
		if bindings[selection.Identity.Run] != selection {
			return domain.ErrChanged
		}
		for _, group := range doc.Runs {
			if group.Selection.Run == selection.Identity.Run {
				for _, record := range group.Receipts {
					receipt, e := record.receipt(selection)
					if e != nil {
						return e
					}
					result = append(result, receipt)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, xerrors.Errorf("read runtime control metadata: %w", err)
	}
	return result, nil
}
