package types

import (
	"slices"
	"strings"

	"golang.org/x/xerrors"
)

// MemoryScopeKind classifies typed memory scopes.
type MemoryScopeKind string

const (
	// MemoryScopeKindWorkspace scopes memory to a workspace.
	MemoryScopeKindWorkspace MemoryScopeKind = "workspace"
	// MemoryScopeKindAgent scopes memory to an agent.
	MemoryScopeKindAgent MemoryScopeKind = "agent"
	// MemoryScopeKindSessionFamily scopes memory to a session family.
	MemoryScopeKindSessionFamily MemoryScopeKind = "session_family"
)

var knownMemoryScopeKinds = []MemoryScopeKind{
	MemoryScopeKindWorkspace,
	MemoryScopeKindAgent,
	MemoryScopeKindSessionFamily,
}

// MemoryScope is a sealed interface for durable memory scope values.
type MemoryScope interface {
	mustEmbedMemoryScope()
	Kind() MemoryScopeKind
	Key() string
}

// MemoryScopeKindOf creates a MemoryScopeKind from a string.
func MemoryScopeKindOf(value string) (MemoryScopeKind, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return MemoryScopeKind(""), xerrors.Errorf("memory scope kind must not be empty")
	}
	if slices.Contains(knownMemoryScopeKinds, MemoryScopeKind(trimmedValue)) {
		return MemoryScopeKind(trimmedValue), nil
	}
	return MemoryScopeKind(""), xerrors.Errorf("unknown memory scope kind: %s", trimmedValue)
}

// String returns the string representation.
func (m MemoryScopeKind) String() string { return string(m) }

// WorkspaceScope constrains memory to a workspace.
type WorkspaceScope struct {
	workspace Workspace
}

// WorkspaceScopeOf creates a WorkspaceScope.
func WorkspaceScopeOf(workspace Workspace) WorkspaceScope {
	return WorkspaceScope{workspace: workspace}
}

func (WorkspaceScope) mustEmbedMemoryScope() {}

// Kind returns the scope kind.
func (s WorkspaceScope) Kind() MemoryScopeKind { return MemoryScopeKindWorkspace }

// Key returns the flattened key representation.
func (s WorkspaceScope) Key() string { return s.workspace.String() }

// Workspace returns the workspace value.
func (s WorkspaceScope) Workspace() Workspace { return s.workspace }

// AgentScope constrains memory to an agent.
type AgentScope struct {
	agent Agent
}

// AgentScopeOf creates an AgentScope.
func AgentScopeOf(agent Agent) AgentScope {
	return AgentScope{agent: agent}
}

func (AgentScope) mustEmbedMemoryScope() {}

// Kind returns the scope kind.
func (s AgentScope) Kind() MemoryScopeKind { return MemoryScopeKindAgent }

// Key returns the flattened key representation.
func (s AgentScope) Key() string { return s.agent.String() }

// Agent returns the agent value.
func (s AgentScope) Agent() Agent { return s.agent }

// SessionFamilyScope constrains memory to a session family.
type SessionFamilyScope struct {
	sessionID SessionID
}

// SessionFamilyScopeOf creates a SessionFamilyScope.
func SessionFamilyScopeOf(sessionID SessionID) SessionFamilyScope {
	return SessionFamilyScope{sessionID: sessionID}
}

func (SessionFamilyScope) mustEmbedMemoryScope() {}

// Kind returns the scope kind.
func (s SessionFamilyScope) Kind() MemoryScopeKind { return MemoryScopeKindSessionFamily }

// Key returns the flattened key representation.
func (s SessionFamilyScope) Key() string { return s.sessionID.String() }

// SessionID returns the session family identifier.
func (s SessionFamilyScope) SessionID() SessionID { return s.sessionID }

// MemoryScopeFrom restores a typed MemoryScope from persisted values.
func MemoryScopeFrom(kind string, value string) (MemoryScope, error) {
	resolvedKind, err := MemoryScopeKindOf(kind)
	if err != nil {
		return nil, err
	}

	switch resolvedKind {
	case MemoryScopeKindWorkspace:
		workspace, err := WorkspaceOf(value)
		if err != nil {
			return nil, xerrors.Errorf("invalid workspace scope: %w", err)
		}
		return WorkspaceScopeOf(workspace), nil
	case MemoryScopeKindAgent:
		agent, err := AgentOf(value)
		if err != nil {
			return nil, xerrors.Errorf("invalid agent scope: %w", err)
		}
		return AgentScopeOf(agent), nil
	case MemoryScopeKindSessionFamily:
		sessionID, err := SessionIDOf(value)
		if err != nil {
			return nil, xerrors.Errorf("invalid session family scope: %w", err)
		}
		return SessionFamilyScopeOf(sessionID), nil
	default:
		return nil, xerrors.Errorf("unsupported memory scope kind: %s", resolvedKind)
	}
}
