package runtime

import (
	app "github.com/duck8823/traceary/application/runtime"
	domain "github.com/duck8823/traceary/domain/runtime"
	"time"
)

type historyAnchorRecord struct {
	At string `json:"at"`
	ID string `json:"id"`
}
type historySelectionRecord struct {
	From    string               `json:"from"`
	To      string               `json:"to"`
	Limit   int                  `json:"limit"`
	Before  *historyAnchorRecord `json:"before,omitempty"`
	Witness string               `json:"witness,omitempty"`
}
type historyRequestRecord struct {
	Epoch     string                 `json:"epoch"`
	Worktree  string                 `json:"worktree"`
	Selection historySelectionRecord `json:"selection"`
}
type historySourceRecord struct {
	Worktree worktreeRecord `json:"worktree"`
	Witness  string         `json:"witness"`
}
type historyEventRecord struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Client    string `json:"client"`
	Agent     string `json:"agent"`
	Session   string `json:"session"`
	Workspace string `json:"workspace"`
	Hook      string `json:"hook"`
	At        string `json:"at"`
}
type historyPageRecord struct {
	Events []historyEventRecord `json:"events"`
	More   *bool                `json:"more"`
	Next   *historyAnchorRecord `json:"next,omitempty"`
}
type historyHintRecord struct {
	Revision string `json:"revision"`
	Overflow *bool  `json:"overflow"`
}
type historyResultRecord struct {
	Request historyRequestRecord `json:"request"`
	Source  historySourceRecord  `json:"source"`
	Page    historyPageRecord    `json:"page"`
	Hint    historyHintRecord    `json:"hint"`
}

func parseHistoryTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil || t.UTC().Format(time.RFC3339Nano) != s || t.IsZero() || t.Year() < 1 || t.Year() > 9999 {
		return time.Time{}, domain.ErrInvalid
	}
	return t, nil
}
func recordHistoryAnchor(a domain.HistoryAnchor) *historyAnchorRecord {
	if a.IsZero() {
		return nil
	}
	return &historyAnchorRecord{a.At.UTC().Format(time.RFC3339Nano), a.ID}
}
func (a *historyAnchorRecord) anchor() (domain.HistoryAnchor, error) {
	if a == nil {
		return domain.HistoryAnchor{}, nil
	}
	t, err := parseHistoryTime(a.At)
	if err != nil {
		return domain.HistoryAnchor{}, domain.ErrInvalid
	}
	return domain.HistoryAnchor{At: t, ID: a.ID}, nil
}
func recordHistoryRequest(r app.RemoteHistoryRequest) historyRequestRecord {
	v := r.Request.Selection
	return historyRequestRecord{r.Epoch, string(r.Request.Worktree), historySelectionRecord{v.From().Format(time.RFC3339Nano), v.To().Format(time.RFC3339Nano), v.Limit(), recordHistoryAnchor(v.Before()), r.Request.ExpectedWitness}}
}
func (r historyRequestRecord) request() (app.RemoteHistoryRequest, error) {
	from, err := parseHistoryTime(r.Selection.From)
	if err != nil {
		return app.RemoteHistoryRequest{}, domain.ErrInvalid
	}
	to, err := parseHistoryTime(r.Selection.To)
	if err != nil {
		return app.RemoteHistoryRequest{}, domain.ErrInvalid
	}
	before, err := r.Selection.Before.anchor()
	if err != nil {
		return app.RemoteHistoryRequest{}, domain.ErrInvalid
	}
	v, err := domain.HistorySelectionOf(from, to, r.Selection.Limit, before)
	if err != nil {
		return app.RemoteHistoryRequest{}, domain.ErrInvalid
	}
	result := app.RemoteHistoryRequest{Epoch: r.Epoch, Request: app.HistoryRequest{Worktree: domain.WorktreeID(r.Worktree), Selection: v, ExpectedWitness: r.Selection.Witness}}
	if !result.Valid() {
		return app.RemoteHistoryRequest{}, domain.ErrInvalid
	}
	return result, nil
}
func recordHistoryHint(h app.HistoryHintState) historyHintRecord {
	overflow := h.Overflow
	return historyHintRecord{domain.ByteOffset(h.Revision).String(), &overflow}
}
func (r historyHintRecord) hint() (app.HistoryHintState, error) {
	n, err := domain.ParseByteOffset(r.Revision)
	if r.Overflow == nil {
		return app.HistoryHintState{}, domain.ErrInvalid
	}
	h := app.HistoryHintState{Revision: uint64(n), Overflow: *r.Overflow}
	if err != nil || !h.Valid() {
		return app.HistoryHintState{}, domain.ErrInvalid
	}
	return h, nil
}
func recordHistoryResult(request app.RemoteHistoryRequest, result app.HistoryResult) *historyResultRecord {
	p := result.Page
	more := p.More()
	w := p.Source().Worktree()
	r := &historyResultRecord{Request: recordHistoryRequest(request), Source: historySourceRecord{worktreeRecord{string(w.ID()), string(w.Project()), w.Path(), w.CommonDir(), w.Store(), w.Witness()}, p.Source().Witness()}, Page: historyPageRecord{Events: []historyEventRecord{}, More: &more, Next: recordHistoryAnchor(p.Next())}, Hint: recordHistoryHint(result.Hint)}
	for _, e := range p.Events() {
		s := e.Snapshot()
		r.Page.Events = append(r.Page.Events, historyEventRecord{s.ID, s.Kind, s.Client, s.Agent, s.SessionID, s.Workspace, s.SourceHook, s.CreatedAt.Format(time.RFC3339Nano)})
	}
	return r
}
func (r historyResultRecord) result() (app.HistoryResult, error) {
	request, err := r.Request.request()
	if err != nil {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	w := r.Source.Worktree
	worktree, err := domain.WorktreeOf(domain.ProjectID(w.Project), w.Path, w.Common, w.Store, w.Witness)
	if err != nil || string(worktree.ID()) != w.ID || worktree.ID() != request.Request.Worktree {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	source, err := domain.HistorySourceOf(worktree, r.Source.Witness)
	if err != nil {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	if !(app.HistoryRequest{Worktree: worktree.ID(), Selection: request.Request.Selection, ExpectedWitness: source.Witness()}).Valid() {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	if request.Request.ExpectedWitness != "" && request.Request.ExpectedWitness != source.Witness() {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	if r.Page.More == nil || r.Page.Events == nil || len(r.Page.Events) > request.Request.Selection.Limit() {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	events := make([]domain.HistoryEvent, 0, len(r.Page.Events))
	for _, e := range r.Page.Events {
		at, err := parseHistoryTime(e.At)
		if err != nil {
			return app.HistoryResult{}, domain.ErrInvalid
		}
		event, err := domain.HistoryEventOf(domain.HistoryEventState{ID: e.ID, Kind: e.Kind, Client: e.Client, Agent: e.Agent, SessionID: e.Session, Workspace: e.Workspace, SourceHook: e.Hook, CreatedAt: at})
		if err != nil {
			return app.HistoryResult{}, domain.ErrInvalid
		}
		events = append(events, event)
	}
	next, err := r.Page.Next.anchor()
	if err != nil {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	page, err := domain.HistoryPageFrom(domain.HistoryPageState{Source: source, Selection: request.Request.Selection, Events: events, More: *r.Page.More, Next: next})
	if err != nil {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	hint, err := r.Hint.hint()
	if err != nil {
		return app.HistoryResult{}, domain.ErrInvalid
	}
	return app.HistoryResult{Epoch: request.Epoch, Page: page, Hint: hint}, nil
}

func (r runtimeRequest) singleArm() bool {
	count := 0
	for _, present := range []bool{r.Control != nil, r.Receipt != nil, r.Attach != nil, r.Observation != nil, r.Launch != nil, r.Page != nil, r.History != nil, r.HistoryHint != nil, r.Lifecycle != nil} {
		if present {
			count++
		}
	}
	return count <= 1 && (r.History == nil || r.Operation == "history_page") && (r.HistoryHint == nil || r.Operation == "history_hint")
}
