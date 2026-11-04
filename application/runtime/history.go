package runtime

import (
	"context"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"

	domain "github.com/duck8823/traceary/domain/runtime"
	"golang.org/x/xerrors"
)

// HistorySources supplies registered read-only evidence, never native authority.
type HistorySources interface {
	ResolveHistorySource(context.Context, domain.WorktreeID) (domain.HistorySource, error)
	RevalidateHistorySource(context.Context, domain.HistorySource) error
}

// HistoryReader must close all SQL resources/leases before returning, including errors.
type HistoryReader interface {
	ReadHistoryPage(context.Context, domain.HistorySource, domain.HistorySelection) ([]domain.HistoryEvent, error)
}

// HistoryRequest binds a requested view and any continuation to its physical source.
type HistoryRequest struct {
	Worktree        domain.WorktreeID
	Selection       domain.HistorySelection
	ExpectedWitness string
}

// Valid rejects partial/unbound continuation and arbitrary target identifiers.
func (r HistoryRequest) Valid() bool {
	id := string(r.Worktree)
	if len(id) != 66 || !strings.HasPrefix(id, "w_") || !r.Selection.Valid() {
		return false
	}
	if _, err := hex.DecodeString(id[2:]); err != nil || strings.ToLower(id) != id {
		return false
	}
	if !utf8.ValidString(r.ExpectedWitness) {
		return false
	}
	for _, c := range r.ExpectedWitness {
		if unicode.IsControl(c) {
			return false
		}
	}
	return (r.Selection.Before().IsZero() || r.ExpectedWitness != "") && len(r.ExpectedWitness) <= 1024 && !strings.ContainsAny(r.ExpectedWitness, "\x00\r\n")
}

// History orchestrates one committed body-free page, not live agent knowledge.
type History struct {
	sources HistorySources
	reader  HistoryReader
}

// NewHistory assembles separate source and bounded-reader capabilities.
func NewHistory(sources HistorySources, reader HistoryReader) (*History, error) {
	if sources == nil || reader == nil {
		return nil, domain.ErrInvalid
	}
	return &History{sources, reader}, nil
}

// Page never retries a busy reader, retains a SQL lease, or advances a commit cursor.
func (s *History) Page(ctx context.Context, r HistoryRequest) (domain.HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return domain.HistoryPage{}, xerrors.Errorf("read history page budget: %w", err)
	}
	if !r.Valid() {
		return domain.HistoryPage{}, domain.ErrInvalid
	}
	source, err := s.sources.ResolveHistorySource(ctx, r.Worktree)
	if err != nil {
		if ctx.Err() != nil {
			return domain.HistoryPage{}, xerrors.Errorf("resolve history source budget: %w", ctx.Err())
		}
		return domain.HistoryPage{}, xerrors.Errorf("resolve history source: %w", err)
	}
	if !source.Valid() || source.Worktree().ID() != r.Worktree {
		return domain.HistoryPage{}, domain.ErrChanged
	}
	if r.ExpectedWitness != "" && r.ExpectedWitness != source.Witness() {
		return domain.HistoryPage{}, domain.ErrChanged
	}
	rows, err := s.reader.ReadHistoryPage(ctx, source, r.Selection)
	if err != nil {
		if ctx.Err() != nil {
			return domain.HistoryPage{}, xerrors.Errorf("read history page budget: %w", ctx.Err())
		}
		return domain.HistoryPage{}, xerrors.Errorf("read committed history page: %w", err)
	}
	// The reader has closed its actual lease before potentially slow Git/source I/O.
	if err = s.sources.RevalidateHistorySource(ctx, source); err != nil {
		if ctx.Err() != nil {
			return domain.HistoryPage{}, xerrors.Errorf("revalidate history source budget: %w", ctx.Err())
		}
		return domain.HistoryPage{}, xerrors.Errorf("revalidate history source: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return domain.HistoryPage{}, xerrors.Errorf("finish history page budget: %w", err)
	}
	p, err := domain.HistoryPageOf(source, r.Selection, rows)
	if err != nil {
		return domain.HistoryPage{}, xerrors.Errorf("validate history page: %w", err)
	}
	return p, nil
}

// HistoryFilesystem verifies only the registered Git/worktree and current read store.
type HistoryFilesystem interface {
	ResolveHistorySource(context.Context, domain.Project, domain.Worktree) (domain.HistorySource, error)
}

// RegisteredHistorySources reads the SAME RegistryRepository without rebinding it.
type RegisteredHistorySources struct {
	repository RegistryRepository
	filesystem HistoryFilesystem
}

// NewRegisteredHistorySources creates a narrow read facade, not another target registry.
func NewRegisteredHistorySources(repository RegistryRepository, filesystem HistoryFilesystem) (*RegisteredHistorySources, error) {
	if repository == nil || filesystem == nil {
		return nil, domain.ErrInvalid
	}
	return &RegisteredHistorySources{repository, filesystem}, nil
}

// ResolveHistorySource checks membership before physical evidence; it has no profile port.
func (s *RegisteredHistorySources) ResolveHistorySource(ctx context.Context, id domain.WorktreeID) (domain.HistorySource, error) {
	r, err := s.repository.Load(ctx)
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("read history registry: %w", err)
	}
	w, err := r.Worktree(id)
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("find history worktree: %w", err)
	}
	p, err := r.Project(w.Project())
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("find history project: %w", err)
	}
	source, err := s.filesystem.ResolveHistorySource(ctx, p, w)
	if err != nil {
		return domain.HistorySource{}, xerrors.Errorf("inspect history source: %w", err)
	}
	if !source.Valid() || source.Worktree() != w {
		return domain.HistorySource{}, domain.ErrChanged
	}
	return source, nil
}

// RevalidateHistorySource rejects membership or physical changes; no native authority.
func (s *RegisteredHistorySources) RevalidateHistorySource(ctx context.Context, source domain.HistorySource) error {
	fresh, err := s.ResolveHistorySource(ctx, source.Worktree().ID())
	if err != nil {
		return err
	}
	if fresh != source {
		return domain.ErrChanged
	}
	return nil
}
