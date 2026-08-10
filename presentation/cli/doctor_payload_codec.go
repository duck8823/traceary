package cli

import (
	"context"
)

func (c *RootCLI) inspectPayloadCodec(ctx context.Context, snapshot storeFileSnapshot) doctorCheck {
	const name = "payload-codec"
	if !snapshot.Exists {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("payload codec state will be created with the SQLite store", "SQLite ストアの作成時に payload codec 状態も作成されます")}
	}
	if c.payloadCodecInspector == nil {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: Localize("payload codec state is unavailable", "payload codec 状態を確認できません"), Hint: Localize("rerun doctor with the current Traceary build", "最新の Traceary で doctor を再実行してください")}
	}
	state, err := c.payloadCodecInspector.InspectPayloadCodec(ctx)
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("failed to inspect payload codec state: %v", "payload codec 状態の確認に失敗しました: %v", err)}
	}
	if !state.MetadataAvailable {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("payload codec metadata is not present; this is a legacy store", "payload codec metadata はありません。legacy store です")}
	}
	if state.CompatibilityState == "invalid" {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("payload codec compatibility evidence is invalid (mode=%s); compressed-row counts are unavailable", "payload codec compatibility evidence が無効です（mode=%s）。圧縮行の件数は利用できません", state.CompatibilityMode)}
	}
	if state.CompatibilityMode != "counter" {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("payload codec compatibility evidence uses %s mode; compressed-row counts are unavailable", "payload codec compatibility evidence は %s mode です。圧縮行の件数は利用できません", state.CompatibilityMode)}
	}
	zstdRows := state.EventBodyNonIdentity + state.AuditCommandNonIdentity + state.AuditInputNonIdentity + state.AuditOutputNonIdentity
	if zstdRows == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("payload codec is ready; no compressed rows exist (minimum reader v%d)", "payload codec は準備済みです。圧縮行はまだありません（minimum reader v%d）", state.MinimumReader)}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusPass,
		Message: localizef("payload codec has compressed rows (events.body=%d, command_audits command=%d input=%d output=%d); downgrade to v0.33 or earlier cannot read them", "payload codec に圧縮行があります（events.body=%d、command_audits command=%d input=%d output=%d）。v0.33 以前へ downgrade すると読み取れません", state.EventBodyNonIdentity, state.AuditCommandNonIdentity, state.AuditInputNonIdentity, state.AuditOutputNonIdentity),
	}
}
