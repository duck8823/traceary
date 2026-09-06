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
| `events.body` duplicate share | `< 5%` | 非圧縮 plaintext バイト対同一 `body` ごとの 1 コピー（判定は厳密な `< 0.05`） |
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

## Dogfood 方針

実サイズの dogfood 実行はリリースゲート**ではありません**（2026-09-06 のオーナー判断、#2349）。v0.49.0 の 12 GiB 実コピーでの offline-upgrade dogfood は約 40 GiB の一時領域を要し、約 1 時間かかり、一般的なメンテナマシンでは SQLITE_FULL で失敗しました（#2347）。実質は再現性もプロビジョニングの筋書きもない手動 e2e テストでした。

代わりにリリースごとに MUST として担保するもの：

1. Plugin identity: `scripts/verify-post-upgrade-plugin-refresh.sh` — インストール済み全 host パッケージがリリース binary と一致すること。
2. Live record: `scripts/verify-post-upgrade-live-capture.sh` — skip 指定のない全 host が使い捨て store に `session_started` + `prompt` を記録すること。
3. Read-side guarantee: `scripts/verify-record-search-refine.sh` — 境界付き使い捨て store（64 MiB 未満、終了時削除）での synthetic な record / search / session-refine / memory の往復。

実サイズの実行は opt-in のみ：ディスクを事前に確保し、証跡を残し、ゲートにはしません。[post-upgrade plugin refresh](./post-upgrade-plugins.ja.md) を参照してください。

## Rebuild

search-projection family は v0.49.0（#2319）で削除されました。`store compact --projection-rebuild` / `--projection-abort` は unknown flag です。検索は two-tier 読み取り経路です。旧 family の offline DROP + VACUUM は `traceary doctor --fix`（verified candidate。store open では走りません）。[検索プロジェクションの再構築](../search-projection-rebuild.ja.md) を参照してください。

#2265 のプロジェクション再構築完了ゲート（`scripts/verify-projection-completion.sh`）は退役です。`search_projection_session_keywords` と `literal_search_fingerprints` の `WITHOUT ROWID` 変換（#2266）は出荷しません。
