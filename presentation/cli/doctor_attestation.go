package cli

import (
	"context"

	"github.com/duck8823/traceary/application"
	"github.com/duck8823/traceary/domain/attestation"
)

func (c *RootCLI) inspectAttestationAnchor(ctx context.Context, storePath string, openStore bool) doctorCheck {
	const name = "attestation-anchor"
	if c.attestationAnchorInspector == nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: Localize("attestation anchor inspector is unavailable", "attestation anchor inspector がありません"),
		}
	}
	state, err := c.attestationAnchorInspector.InspectAttestationAnchor(ctx, application.AttestationAnchorInspectOptions{
		StorePath: storePath,
		OpenStore: openStore,
	})
	if err != nil {
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Message: localizef("attestation anchor check failed: %v", "attestation anchor の確認に失敗しました: %v", err),
		}
	}
	switch state.Relation {
	case string(attestation.AnchorMismatch), string(attestation.AnchorAhead), "chain_broken":
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusFail,
			Message: localizef("attestation anchor does not match the store (relation=%s file_seq=%s store_seq=%s)", "attestation anchor が store と一致しません（relation=%s file_seq=%s store_seq=%s）", state.Relation, attestation.FormatSeq(state.FileSeq), attestation.FormatSeq(state.StoreSeq)),
		}
	case string(attestation.AnchorMissing):
		if !openStore {
			return doctorCheck{
				Name:    name,
				Status:  doctorStatusWarn,
				Message: localizef("attestation anchor file is absent (%s); large-store doctor does not publish it", "attestation anchor ファイルがありません（%s）。大容量 doctor では作成しません", state.Path),
			}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusWarn,
			Message: localizef("attestation anchor file is absent (%s)", "attestation anchor ファイルがありません（%s）", state.Path),
		}
	case "file_ok":
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: localizef("attestation anchor file is readable (seq=%s path=%s)", "attestation anchor ファイルを読めます（seq=%s path=%s）", attestation.FormatSeq(state.FileSeq), state.Path),
		}
	default:
		if state.Published {
			return doctorCheck{
				Name:    name,
				Status:  doctorStatusPass,
				Message: localizef("attestation anchor published (seq=%s path=%s)", "attestation anchor を書きました（seq=%s path=%s）", attestation.FormatSeq(state.StoreSeq), state.Path),
			}
		}
		return doctorCheck{
			Name:    name,
			Status:  doctorStatusPass,
			Message: localizef("attestation anchor matches the store head (seq=%s)", "attestation anchor は store head と一致しています（seq=%s）", attestation.FormatSeq(state.StoreSeq)),
		}
	}
}
