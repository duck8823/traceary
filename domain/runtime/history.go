package runtime

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxHistoryPageEvents bounds a body-free page, not the whole requested view.
const MaxHistoryPageEvents = 64

// HistoryAnchor is an existing descending (timestamp, ID) keyset, not commit order.
type HistoryAnchor struct {
	At time.Time
	ID string
}

// IsZero reports absence; partial anchors are rejected by HistorySelectionOf.
func (a HistoryAnchor) IsZero() bool { return a.At.IsZero() && a.ID == "" }

func validHistoryTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }
func historyText(s string, required bool) bool {
	if len(s) > 256 || !utf8.ValidString(s) || required && strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func historyIdentifier(s string, required bool) bool {
	return historyText(s, required) && strings.TrimSpace(s) == s
}

// HistorySelection is one finite requested window and one bounded keyset page.
type HistorySelection struct {
	from, to time.Time
	limit    int
	before   HistoryAnchor
}

// HistorySelectionOf validates explicit bounds without inventing a durable watermark.
func HistorySelectionOf(from, to time.Time, limit int, before HistoryAnchor) (HistorySelection, error) {
	from = from.UTC().Round(0)
	to = to.UTC().Round(0)
	if !before.IsZero() {
		before.At = before.At.UTC().Round(0)
	}
	if !validHistoryTime(from) || !validHistoryTime(to) || !from.Before(to) || limit < 1 || limit > MaxHistoryPageEvents {
		return HistorySelection{}, ErrInvalid
	}
	if !before.IsZero() && (!validHistoryTime(before.At) || !historyIdentifier(before.ID, true) || before.At.Before(from) || !before.At.Before(to)) {
		return HistorySelection{}, ErrInvalid
	}
	return HistorySelection{from, to, limit, before}, nil
}

// From is inclusive.
func (v HistorySelection) From() time.Time { return v.from }

// To is exclusive.
func (v HistorySelection) To() time.Time { return v.to }

// Limit is the requested page size; readers fetch at most Limit+1.
func (v HistorySelection) Limit() int { return v.limit }

// Before is the optional complete previous-page keyset anchor.
func (v HistorySelection) Before() HistoryAnchor { return v.before }

// Valid rejects unconstructed selections at capability boundaries.
func (v HistorySelection) Valid() bool {
	_, err := HistorySelectionOf(v.from, v.to, v.limit, v.before)
	return err == nil
}

// HistorySource is registered read-only scope plus this read's physical witness.
// It never validates native executable/profile/session or grants control authority.
type HistorySource struct {
	worktree Worktree
	witness  string
}

// HistorySourceOf reconstructs trusted read evidence, never Registry membership.
func HistorySourceOf(w Worktree, witness string) (HistorySource, error) {
	valid, err := WorktreeOf(w.project, w.path, w.common, w.store, w.witness)
	if err != nil || valid != w || !validWitness(witness) {
		return HistorySource{}, ErrInvalid
	}
	return HistorySource{w, witness}, nil
}

// Worktree is the immutable registered selection, never rebound after compaction.
func (s HistorySource) Worktree() Worktree { return s.worktree }

// Witness identifies the physical source seen for this read only.
func (s HistorySource) Witness() string { return s.witness }

// StoreIdentityChanged is not native-target revalidation.
func (s HistorySource) StoreIdentityChanged() bool { return s.witness != s.worktree.Witness() }

// Valid rejects unconstructed source evidence.
func (s HistorySource) Valid() bool {
	_, err := HistorySourceOf(s.worktree, s.witness)
	return err == nil
}

// HistoryEventState is body-free committed history, not live interaction state.
type HistoryEventState struct {
	ID, Kind, Client, Agent, SessionID, Workspace, SourceHook string
	CreatedAt                                                 time.Time
}

// HistoryEvent owns validated projection fields; contents cannot be edited in place.
type HistoryEvent struct{ state HistoryEventState }

// HistoryEventOf refuses malformed/oversized rows instead of silently skipping them.
func HistoryEventOf(s HistoryEventState) (HistoryEvent, error) {
	s.CreatedAt = s.CreatedAt.UTC().Round(0)
	if !historyIdentifier(s.ID, true) || !historyIdentifier(s.Kind, true) || !historyText(s.Client, false) || !historyIdentifier(s.Agent, false) || !historyIdentifier(s.SessionID, false) || !historyText(s.SourceHook, false) || !validPath(s.Workspace) || !validHistoryTime(s.CreatedAt) {
		return HistoryEvent{}, ErrInvalid
	}
	s.CreatedAt = s.CreatedAt.UTC().Round(0)
	return HistoryEvent{s}, nil
}

// Snapshot returns a value copy without core bodies or command contents.
func (e HistoryEvent) Snapshot() HistoryEventState { return e.state }
func historyAfter(a, b HistoryAnchor) bool {
	return a.At.After(b.At) || a.At.Equal(b.At) && a.ID > b.ID
}
func (e HistoryEvent) anchor() HistoryAnchor { return HistoryAnchor{e.state.CreatedAt, e.state.ID} }

// HistoryPage is one committed page, not a frozen cross-page snapshot.
type HistoryPage struct {
	source    HistorySource
	selection HistorySelection
	events    []HistoryEvent
	more      bool
	next      HistoryAnchor
}

// HistoryPageOf checks every fetched row (including the look-ahead) before trimming.
func HistoryPageOf(source HistorySource, selection HistorySelection, rows []HistoryEvent) (HistoryPage, error) {
	if !source.Valid() || !selection.Valid() {
		return HistoryPage{}, ErrInvalid
	}
	if len(rows) > selection.limit+1 {
		return HistoryPage{}, ErrLimit
	}
	seen := make(map[string]bool, len(rows))
	for i, e := range rows {
		state := e.Snapshot()
		if _, err := HistoryEventOf(state); err != nil {
			return HistoryPage{}, err
		}
		if state.Workspace != source.Worktree().Path() || state.CreatedAt.Before(selection.from) || !state.CreatedAt.Before(selection.to) || seen[state.ID] {
			return HistoryPage{}, ErrInvalid
		}
		if !selection.before.IsZero() && !historyAfter(selection.before, e.anchor()) || i > 0 && !historyAfter(rows[i-1].anchor(), e.anchor()) {
			return HistoryPage{}, ErrInvalid
		}
		seen[state.ID] = true
	}
	p := HistoryPage{source: source, selection: selection, events: append([]HistoryEvent{}, rows...)}
	if len(rows) > selection.limit {
		return p.Prefix(selection.limit)
	}
	return p, nil
}

// Source is current read-only source evidence.
func (p HistoryPage) Source() HistorySource { return p.source }

// Selection is the exact requested view/anchor/limit.
func (p HistoryPage) Selection() HistorySelection { return p.selection }

// Events returns a defensive copy; empty pages have a nonnil empty slice.
func (p HistoryPage) Events() []HistoryEvent { return append([]HistoryEvent{}, p.events...) }

// More requires a continuation from Next, not a claim of complete global history.
func (p HistoryPage) More() bool { return p.more }

// Next refers to the last retained row, including after transport frame trimming.
func (p HistoryPage) Next() HistoryAnchor { return p.next }

// Prefix trims a frame-sized prefix without losing the next row's continuation.
func (p HistoryPage) Prefix(limit int) (HistoryPage, error) {
	if !p.source.Valid() || !p.selection.Valid() || limit < 1 || limit > len(p.events) {
		return HistoryPage{}, ErrInvalid
	}
	if limit < len(p.events) {
		p.more = true
	}
	p.events = append([]HistoryEvent{}, p.events[:limit]...)
	if p.more {
		p.next = p.events[len(p.events)-1].anchor()
	}
	return p, nil
}
