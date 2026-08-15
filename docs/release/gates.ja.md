# リリースゲートと計測値

[English](./gates.md)

**Issue:** #1873（#1620 の続き）

自動で評価され、miss がリリースを落とせる行だけを **gate** と呼びます。CI は生成した fixture store 上の `go test` で評価します。メンテナは次も使えます。

```sh
go run ./cmd/repo-tooling release evaluate-gates --db /path/to/fixture.db
```

既定の live path（`~/.config/traceary/traceary.db`）は拒否します。fixture か operator copy が必要です。live store は外れ値であり、リリース用 corpus ではありません（#1863）。

## Gates

次の 6 行を評価します。`skip` は miss ではありません。

| Gate | 閾値 | 測り方 |
|---|---|---|
| event-emission amplification | `<= 2.0` | events / (`prompt` + `command_executed`) |
| whole-store amplification | `<= 3x` | `OperatorCostInspector.Amplification`（resident / retained source bytes） |
| recent search-index amplification | `<= 4x` | complete な世代が measured な `recent_amplification_ppm` を残しているとき。それ以外は skip |
| `events.body` duplicate share | `< 5%` | 非圧縮 plaintext バイト対 `body_sha256` ごとの 1 コピー（判定は厳密な `< 0.05`） |
| refinement coverage | worth folding の `>= 95%` | `#1879` `FoldGateInspector` |
| wake injection | 適格 host ごとに budget 内 | `#1879` `FoldGateInspector`（適格 host が無いときは unmeasured / skip） |

search-index rebuild 中の peak resident と recent-index-tier の構造行は #1751 / #1753 のままです。ここでの gate ではありません。

## Measurements

#1620 の絶対バイト 5 行は **gate ではありません**。特定のバイト数が正しいという導出はありません。由来した corpus を付けて公開し、リリースを落としません。

Corpus: **maintainer store 2026-08-11 uncompressed #1620**。

| Measurement | 公開している目安 |
|---|---|
| undiscardable growth | 正規操作あたり `<= 5 KiB` |
| command record | command execution あたり `<= 4 KiB` |
| prompt record | user turn あたり `<= 12 KiB` |
| session-tier coefficient | session あたり `<= 64 KiB` |
| resident store | 正規操作あたり `<= 13 KiB` |

## Rebuild

オペレータ向けの **rebuild** は **search-index family** の再構築（`traceary store search-projection …`）です。コンパイル手順でも、ストア全体の rebuild でもありません。[検索プロジェクションの再構築](../search-projection-rebuild.ja.md) を参照してください。
