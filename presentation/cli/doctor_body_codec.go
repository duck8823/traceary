package cli

import (
	"context"
	"fmt"
	"strings"
)

func (c *RootCLI) inspectBodyCodec(ctx context.Context) doctorCheck {
	const name = "body-codec"
	if c.bodyCodecChecker == nil {
		return doctorCheck{Name: name, Status: doctorStatusSkip, Message: Localize("body codec checker is not configured", "body codec チェッカーが設定されていません")}
	}
	state, err := c.bodyCodecChecker.CheckBodyCodec(ctx)
	if err != nil {
		return doctorCheck{Name: name, Status: doctorStatusWarn, Message: localizef("failed to check body codec: %v", "body codec の確認に失敗しました: %v", err)}
	}
	if len(state.UnknownRows) == 0 {
		return doctorCheck{Name: name, Status: doctorStatusPass, Message: Localize("all events use a supported body codec", "すべてのイベントはサポートされた body codec を使用しています")}
	}
	parts := make([]string, 0, len(state.UnknownRows))
	for _, row := range state.UnknownRows {
		parts = append(parts, fmt.Sprintf("%s (count=%d, sample_ids=[%s])", row.Codec, row.Count, strings.Join(row.SampleIDs, ",")))
	}
	return doctorCheck{
		Name:    name,
		Status:  doctorStatusWarn,
		Message: localizef("events contain unsupported body_codec values: %s", "events にサポートされていない body_codec 値があります: %s", strings.Join(parts, "; ")),
		Hint:    Localize("rebuild the store with a binary that supports these codecs, or file a bug against the writer", "これらの codec をサポートするバイナリで store を再構築するか、writer に対してバグ報告してください"),
	}
}
