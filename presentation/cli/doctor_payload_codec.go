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
	zstdRows := state.EventBodyZstd + state.AuditCommandZstd + state.AuditInputZstd + state.AuditOutputZstd
	if zstdRows == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: localizef("payload codec is ready; no compressed rows exist (minimum reader v%d)", "payload codec は準備済みです。圧縮行はまだありません（minimum reader v%d）", state.MinimumReader)}
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusWarn,
		Message: localizef("payload codec has compressed rows (events.body=%d, command_audits command=%d input=%d output=%d); downgrade to v0.33 or earlier cannot read them", "payload codec に圧縮行があります（events.body=%d、command_audits command=%d input=%d output=%d）。v0.33 以前へ downgrade すると読み取れません", state.EventBodyZstd, state.AuditCommandZstd, state.AuditInputZstd, state.AuditOutputZstd),
		Hint:    Localize("once compressed rows exist, run `traceary store backup` before upgrading to keep a fallback", "圧縮行が存在する場合は、fallback を残すため upgrade 前に `traceary store backup` を実行してください"),
	}
}
