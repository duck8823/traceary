// Package runtime owns headless execution identities and registry invariants.
package runtime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// Registry admission limits are fixed by the accepted R0 contract.
const (
	MaxProjects  = 100
	MaxWorktrees = 1000
)

// Registry errors deliberately omit paths, credentials and native payloads.
var (
	ErrInvalid     = errors.New("invalid runtime registry value")
	ErrDuplicate   = errors.New("runtime registry entry already exists")
	ErrNotFound    = errors.New("runtime registry entry not found")
	ErrForeign     = errors.New("worktree does not belong to the project")
	ErrChanged     = errors.New("runtime target or registry changed")
	ErrLimit       = errors.New("runtime registry limit exceeded")
	ErrInUse       = errors.New("runtime project still has registered worktrees")
	ErrUnsafe      = errors.New("unsafe runtime filesystem identity")
	ErrUnsupported = errors.New("unsupported runtime metadata version")
)

// ProjectID identifies a canonical Git common directory and its physical witness.
type ProjectID string

// WorktreeID identifies an immutable canonical worktree/store binding.
type WorktreeID string

// ProfileID is an operator-registered host profile reference, not authorization.
type ProfileID string

// ProfileIDFrom validates a reference without claiming the profile exists.
func ProfileIDFrom(value string) (ProfileID, error) {
	if len(value) == 0 || len(value) > 128 {
		return "", ErrInvalid
	}
	for _, r := range value {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
		if !valid {
			return "", ErrInvalid
		}
	}
	return ProfileID(value), nil
}

func identity(prefix string, values ...string) string {
	h := sha256.New()
	for _, v := range values {
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte{0})
	}
	return prefix + hex.EncodeToString(h.Sum(nil))
}
func validPath(s string) bool {
	return utf8.ValidString(s) && filepath.IsAbs(s) && filepath.Clean(s) == s && len(s) <= 4096 && !strings.ContainsAny(s, "\x00\r\n")
}
func validWitness(s string) bool {
	return utf8.ValidString(s) && len(s) > 0 && len(s) <= 1024 && !strings.ContainsAny(s, "\x00\r\n")
}

// Project is a canonical project identity. Its state cannot be rebound.
type Project struct {
	id                    ProjectID
	root, common, witness string
}

// ProjectOf constructs a project from already resolved filesystem evidence.
func ProjectOf(root, common, witness string) (Project, error) {
	if !validPath(root) || !validPath(common) || !validWitness(witness) {
		return Project{}, ErrInvalid
	}
	return Project{ProjectID(identity("p_", root, common, witness)), root, common, witness}, nil
}

// ID returns the immutable project identity.
func (p Project) ID() ProjectID { return p.id }

// Root returns the canonical registration worktree root.
func (p Project) Root() string { return p.root }

// CommonDir returns the canonical Git common directory.
func (p Project) CommonDir() string { return p.common }

// Witness returns infrastructure-provided physical identity evidence.
func (p Project) Witness() string { return p.witness }

// Worktree binds one registered Git worktree to a core-store identity.
type Worktree struct {
	id                           WorktreeID
	project                      ProjectID
	path, common, store, witness string
}

// WorktreeOf constructs a worktree from resolved, non-secret identity evidence.
func WorktreeOf(project ProjectID, path, common, store, witness string) (Worktree, error) {
	if len(project) != 66 || !strings.HasPrefix(string(project), "p_") || !validPath(path) || !validPath(common) || !validPath(store) || !validWitness(witness) {
		return Worktree{}, ErrInvalid
	}
	return Worktree{WorktreeID(identity("w_", string(project), path, common, store, witness)), project, path, common, store, witness}, nil
}

// ID returns the immutable worktree identity.
func (w Worktree) ID() WorktreeID { return w.id }

// Project returns the parent identity.
func (w Worktree) Project() ProjectID { return w.project }

// Path returns the canonical worktree root.
func (w Worktree) Path() string { return w.path }

// CommonDir returns the canonical Git common directory.
func (w Worktree) CommonDir() string { return w.common }

// Store returns the canonical existing core-store path.
func (w Worktree) Store() string { return w.store }

// Witness returns physical worktree/store evidence, not authority.
func (w Worktree) Witness() string { return w.witness }

// Target is an immutable launch selection; different profiles may share a worktree.
type Target struct {
	worktree Worktree
	profile  ProfileID
}

// Worktree returns the pinned worktree/store snapshot.
func (t Target) Worktree() Worktree { return t.worktree }

// Profile returns the selected profile, which its owner must validate before launch.
func (t Target) Profile() ProfileID { return t.profile }

// RegistryState is a transport-independent, defensive snapshot for persistence/export.
type RegistryState struct {
	Revision  uint64
	Projects  []Project
	Worktrees []Worktree
}

// Registry owns membership, uniqueness, admission and explicit deletion rules.
type Registry struct {
	revision  uint64
	projects  map[ProjectID]Project
	worktrees map[WorktreeID]Worktree
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{projects: make(map[ProjectID]Project), worktrees: make(map[WorktreeID]Worktree)}
}

// RegistryFrom validates a persisted snapshot without trusting its membership.
func RegistryFrom(s RegistryState) (*Registry, error) {
	r := NewRegistry()
	for _, p := range s.Projects {
		if err := r.AddProject(p); err != nil {
			return nil, err
		}
	}
	for _, w := range s.Worktrees {
		if err := r.AddWorktree(w); err != nil {
			return nil, err
		}
	}
	if s.Revision == 0 && (len(s.Projects) > 0 || len(s.Worktrees) > 0) {
		return nil, ErrInvalid
	}
	r.revision = s.Revision
	return r, nil
}

// Snapshot returns independent sorted slices; callers cannot mutate the aggregate.
func (r *Registry) Snapshot() RegistryState {
	s := RegistryState{Revision: r.revision, Projects: make([]Project, 0, len(r.projects)), Worktrees: make([]Worktree, 0, len(r.worktrees))}
	for _, p := range r.projects {
		s.Projects = append(s.Projects, p)
	}
	for _, w := range r.worktrees {
		s.Worktrees = append(s.Worktrees, w)
	}
	slices.SortFunc(s.Projects, func(a, b Project) int { return strings.Compare(string(a.id), string(b.id)) })
	slices.SortFunc(s.Worktrees, func(a, b Worktree) int { return strings.Compare(string(a.id), string(b.id)) })
	return s
}

// Project finds a registered project.
func (r *Registry) Project(id ProjectID) (Project, error) {
	p, ok := r.projects[id]
	if !ok {
		return Project{}, ErrNotFound
	}
	return p, nil
}

// Worktree finds a registered worktree.
func (r *Registry) Worktree(id WorktreeID) (Worktree, error) {
	w, ok := r.worktrees[id]
	if !ok {
		return Worktree{}, ErrNotFound
	}
	return w, nil
}

// AddProject registers one immutable canonical project.
func (r *Registry) AddProject(p Project) error {
	valid, err := ProjectOf(p.root, p.common, p.witness)
	if err != nil || valid != p {
		return ErrInvalid
	}
	for _, existing := range r.projects {
		if existing.common == p.common || existing.root == p.root {
			return ErrDuplicate
		}
	}
	if len(r.projects) >= MaxProjects || r.revision == math.MaxUint64 {
		return ErrLimit
	}
	r.projects[p.id] = p
	r.revision++
	return nil
}

// AddWorktree registers a canonical worktree without changing any existing target.
func (r *Registry) AddWorktree(w Worktree) error {
	valid, err := WorktreeOf(w.project, w.path, w.common, w.store, w.witness)
	if err != nil || valid != w {
		return ErrInvalid
	}
	p, err := r.Project(w.project)
	if err != nil {
		return err
	}
	if p.common != w.common {
		return ErrForeign
	}
	for _, existing := range r.worktrees {
		if existing.path == w.path {
			return ErrDuplicate
		}
	}
	if len(r.worktrees) >= MaxWorktrees || r.revision == math.MaxUint64 {
		return ErrLimit
	}
	r.worktrees[w.id] = w
	r.revision++
	return nil
}

// Target selects a profile without mutating the registry or any other selection.
func (r *Registry) Target(id WorktreeID, profile string) (Target, error) {
	p, err := ProfileIDFrom(profile)
	if err != nil {
		return Target{}, err
	}
	w, err := r.Worktree(id)
	if err != nil {
		return Target{}, err
	}
	return Target{w, p}, nil
}

// DeletePlan is an immutable revision/content-bound preview, not a deletion side effect.
type DeletePlan struct {
	kind, id string
	revision uint64
	digest   string
}

// Kind returns project or worktree.
func (p DeletePlan) Kind() string { return p.kind }

// ID returns the explicitly selected metadata identity.
func (p DeletePlan) ID() string { return p.id }

// Revision returns the state revision inspected during planning.
func (p DeletePlan) Revision() uint64 { return p.revision }
func (r *Registry) digest() string {
	s := r.Snapshot()
	ids := make([]string, 0, len(s.Projects)+len(s.Worktrees))
	for _, p := range s.Projects {
		ids = append(ids, string(p.id))
	}
	for _, w := range s.Worktrees {
		ids = append(ids, string(w.id))
	}
	return identity("registry_", ids...)
}

// PlanDeleteProject refuses cascading deletion and changes no state.
func (r *Registry) PlanDeleteProject(id ProjectID) (DeletePlan, error) {
	if _, err := r.Project(id); err != nil {
		return DeletePlan{}, err
	}
	for _, w := range r.worktrees {
		if w.project == id {
			return DeletePlan{}, ErrInUse
		}
	}
	return DeletePlan{"project", string(id), r.revision, r.digest()}, nil
}

// PlanDeleteWorktree previews metadata-only removal.
func (r *Registry) PlanDeleteWorktree(id WorktreeID) (DeletePlan, error) {
	if _, err := r.Worktree(id); err != nil {
		return DeletePlan{}, err
	}
	return DeletePlan{"worktree", string(id), r.revision, r.digest()}, nil
}

// ApplyDelete applies only the exact unmodified preview. It never touches a filesystem.
func (r *Registry) ApplyDelete(p DeletePlan) error {
	if p.revision != r.revision || p.digest != r.digest() {
		return ErrChanged
	}
	if r.revision == math.MaxUint64 {
		return ErrLimit
	}
	switch p.kind {
	case "project":
		if _, err := r.PlanDeleteProject(ProjectID(p.id)); err != nil {
			return err
		}
		delete(r.projects, ProjectID(p.id))
	case "worktree":
		if _, err := r.Worktree(WorktreeID(p.id)); err != nil {
			return err
		}
		delete(r.worktrees, WorktreeID(p.id))
	default:
		return ErrInvalid
	}
	r.revision++
	return nil
}
