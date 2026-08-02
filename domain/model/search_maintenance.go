//nolint:revive // Public state-machine names mirror persisted authority and phase values.
package model

import "golang.org/x/xerrors"

// SearchAuthority identifies the persisted implementation used by normal
// event search. It is deliberately independent of projection table presence.
type SearchAuthority string

const (
	SearchAuthorityLegacy SearchAuthority = "legacy"
	SearchAuthorityTiered SearchAuthority = "tiered"
)

// SearchMaintenancePhase records resumable retirement and restoration.
type SearchMaintenancePhase string

const (
	SearchMaintenanceActive    SearchMaintenancePhase = "active"
	SearchMaintenanceRetiring  SearchMaintenancePhase = "retiring"
	SearchMaintenanceRetired   SearchMaintenancePhase = "retired"
	SearchMaintenanceRestoring SearchMaintenancePhase = "restoring"
)

// SearchMaintenance is the authority/maintenance aggregate. Progress is an
// opaque monotonic row cursor owned by the persistence adapter.
type SearchMaintenance struct {
	authority SearchAuthority
	phase     SearchMaintenancePhase
	progress  int64
}

func SearchMaintenanceOf(authority SearchAuthority, phase SearchMaintenancePhase, progress int64) (SearchMaintenance, error) {
	m := SearchMaintenance{authority: authority, phase: phase, progress: progress}
	if err := m.Validate(); err != nil {
		return SearchMaintenance{}, err
	}
	return m, nil
}

func (m SearchMaintenance) Authority() SearchAuthority    { return m.authority }
func (m SearchMaintenance) Phase() SearchMaintenancePhase { return m.phase }
func (m SearchMaintenance) Progress() int64               { return m.progress }

func (m SearchMaintenance) Validate() error {
	if m.progress < 0 {
		return xerrors.New("search maintenance progress must not be negative")
	}
	switch {
	case m.authority == SearchAuthorityLegacy && m.phase == SearchMaintenanceActive:
	case m.authority == SearchAuthorityTiered && (m.phase == SearchMaintenanceRetiring || m.phase == SearchMaintenanceRetired || m.phase == SearchMaintenanceRestoring):
	default:
		return xerrors.Errorf("invalid search maintenance state %s/%s", m.authority, m.phase)
	}
	return nil
}

func (m SearchMaintenance) StartRetire() (SearchMaintenance, error) {
	if m.authority != SearchAuthorityLegacy || m.phase != SearchMaintenanceActive {
		return SearchMaintenance{}, xerrors.Errorf("cannot retire search from %s/%s", m.authority, m.phase)
	}
	return SearchMaintenance{authority: SearchAuthorityTiered, phase: SearchMaintenanceRetiring}, nil
}

func (m SearchMaintenance) FinishRetire() (SearchMaintenance, error) {
	if m.authority != SearchAuthorityTiered || m.phase != SearchMaintenanceRetiring {
		return SearchMaintenance{}, xerrors.Errorf("cannot finish retire from %s/%s", m.authority, m.phase)
	}
	return SearchMaintenance{authority: SearchAuthorityTiered, phase: SearchMaintenanceRetired}, nil
}

func (m SearchMaintenance) StartRestore() (SearchMaintenance, error) {
	if m.authority == SearchAuthorityTiered && m.phase == SearchMaintenanceRestoring {
		return m, nil
	}
	if m.authority != SearchAuthorityTiered || (m.phase != SearchMaintenanceRetired && m.phase != SearchMaintenanceRetiring) {
		return SearchMaintenance{}, xerrors.Errorf("cannot restore search from %s/%s", m.authority, m.phase)
	}
	return SearchMaintenance{authority: SearchAuthorityTiered, phase: SearchMaintenanceRestoring}, nil
}

func (m SearchMaintenance) FinishRestore() (SearchMaintenance, error) {
	if m.authority != SearchAuthorityTiered || m.phase != SearchMaintenanceRestoring {
		return SearchMaintenance{}, xerrors.Errorf("cannot finish restore from %s/%s", m.authority, m.phase)
	}
	return SearchMaintenance{authority: SearchAuthorityLegacy, phase: SearchMaintenanceActive}, nil
}
