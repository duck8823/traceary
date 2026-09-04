# リリースゲートと計測値

[English](./gates.md)

**Issue:** #1873（#1620 の続き）

自動で評価され、miss がリリースを落とせる行だけを **gate** と呼びます。CI は生成した fixture store 上の `go test` で評価します。メンテナは次も使えます。

```sh
go run ./cmd/repo-tooling release evaluate-gates --db /path/to/fixture.db
```

既定の live path（`~/.config/traceary/traceary.db`）は拒否します。fixture か operator copy が必要です。live store は外れ値であり、リリース用 corpus ではありません（#1863）。

## Gates

次の 6 行を `release evaluate-gates` / `go test` が評価します。`skip` は miss ではありません。旧プロジェクション再構築の完了ゲートは search-projection family と一緒に退役しました（#2319）。

| Gate | 閾値 | 測り方 |
|---|---|---|
| event-emission amplification | `<= 2.0` | events / (`prompt` + `command_executed`) |
| whole-store amplification | `<= 3x` | `OperatorCostInspector.Amplification`（resident / retained source bytes） |
| recent search-index amplification | `<= 4x` | #2319 以降は常に skip（`recent index family is no longer stored`） |
| `events.body` duplicate share | `< 5%` | 非圧縮 plaintext バイト対 `body_sha256` ごとの 1 コピー（判定は厳密な `< 0.05`） |
| refinement coverage | worth folding の `>= 95%` | `#1879` `FoldGateInspector` |
| wake injection | 適格 host ごとに budget 内 | `#1879` `FoldGateInspector`（適格 host が無いときは unmeasured / skip） |

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

search-projection family は v0.49.0（#2319）で削除されました。`store compact --projection-rebuild` / `--projection-abort` は unknown flag です。検索は two-tier 読み取り経路です。旧 family の offline DROP + VACUUM は `traceary doctor --fix`（verified candidate。store open では走りません）。[検索プロジェクションの再構築](../search-projection-rebuild.ja.md) を参照してください。

#2265 のプロジェクション再構築完了ゲート（`scripts/verify-projection-completion.sh`）は退役です。`search_projection_session_keywords` と `literal_search_fingerprints` の `WITHOUT ROWID` 変換（#2266）は出荷しません。
