package model_test

import (
	"testing"

	"github.com/duck8823/traceary/domain/model"
)

func TestSearchMaintenanceLifecycle(t *testing.T) {
	state, err := model.SearchMaintenanceOf(model.SearchAuthorityLegacy, model.SearchMaintenanceActive, 0)
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.StartRetire()
	if err != nil {
		t.Fatal(err)
	}
	if state.Authority() != model.SearchAuthorityTiered || state.Phase() != model.SearchMaintenanceRetiring {
		t.Fatalf("retire state=%s/%s", state.Authority(), state.Phase())
	}
	state, err = state.FinishRetire()
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.StartRestore()
	if err != nil {
		t.Fatal(err)
	}
	state, err = state.FinishRestore()
	if err != nil {
		t.Fatal(err)
	}
	if state.Authority() != model.SearchAuthorityLegacy || state.Phase() != model.SearchMaintenanceActive {
		t.Fatalf("restored=%s/%s", state.Authority(), state.Phase())
	}
}

func TestSearchMaintenanceRejectsImpossibleStates(t *testing.T) {
	for _, tc := range []struct {
		authority model.SearchAuthority
		phase     model.SearchMaintenancePhase
	}{
		{model.SearchAuthorityTiered, model.SearchMaintenanceActive},
		{model.SearchAuthorityLegacy, model.SearchMaintenanceRetired},
		{"unknown", model.SearchMaintenanceActive},
	} {
		if _, err := model.SearchMaintenanceOf(tc.authority, tc.phase, 0); err == nil {
			t.Fatalf("accepted %s/%s", tc.authority, tc.phase)
		}
	}
}
