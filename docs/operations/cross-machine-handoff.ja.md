# マシン間ハンドオフ

[English](./cross-machine-handoff.md)

#567 の一部 · #572 を close。

Traceary は local-first かつ single-SQLite です。`traceary bundle export` / `bundle import` は v0.9.0 で導入された可搬プリミティブで、hosted 面を追加せずに laptop ↔ work desktop ↔ remote dev box 間で履歴を運ぶためのものです。

## bundle の中身

現在の bundle (manifest_version = 2):

- `manifest.json` — store schema version、作成時刻、使用 filter、writer metadata、import defaults、`{table_name, file, row_count, checksum}` を持つ table registry (`tables`)。
- `events.ndjson` — `--since` / `--until` / `--workspace` に一致する event。決定的な出力にするため `created_at` 順。
- `sessions.ndjson` — session 境界レコード。export の window/workspace filter に一致する session に加え、export された event が参照する session も含め、import 後も event が所属 session を保てるようにする。
- `command_audits.ndjson` — shell コマンド監査レコード。export された event に絞り込まれる。
- `memories.ndjson` — scope、validity window、supersession pointer、evidence refs、artifact refs を含む durable memories。
- `memory_edges.ndjson` — `id`、`from_memory_id`、`to_memory_id`、`relation_type`、validity window、`created_at` を含む memory graph edge。
- `run_lineages.ndjson` — host 名前空間付きの不変な run identity、任意の親/work 相関、および本文を含まない packet/tool byte 情報。filter 付き export は usage が参照する run と完全な祖先連鎖だけを含み、filter なしでは単独 run も含める。
- `usage_observations.ndjson` — 任意の run attribution を持つ provider-neutral な usage 証跡。packet 本文、prompt、response、tool 名、引数、結果は含めない。

Traceary は `file_checksums` を使う v0.9.0 `manifest_version = 1` bundle も引き続き import できます。v2 は `tables` で table file を登録します。現在の writer は run fact が 0 件でも空の `run_lineages` を含む 7 table entry を常に出力します。新しい reader はこの table がない旧 v2 bundle を受け入れ、古い reader は未知の table を無視せず拒否します。

## 暗号化

すべての bundle は XChaCha20-Poly1305 で暗号化します。対称鍵は Argon2id (OWASP の既定: 3 iterations、64 MiB、4 lanes) で passphrase から導出します。envelope の先頭には magic bytes `TRBUNDLE` が入り、誤ってリネームされた `.tar.gz` とすぐに区別がつきます。

passphrase は **環境変数のみ** で渡します。CLI フラグ経由では受け付けません。shell history や audit log に機密が残らないためです。

```sh
export TRACEARY_BUNDLE_PASSPHRASE='長めで一意なもの'
```

## 転送

Traceary 自身は bundle ファイルを運びません。すでに 2 台の間にある transport を選んでください:

| 手段 | 向き |
|---|---|
| AirDrop (macOS) | 一度きり、同室、信頼できる最速経路 |
| `scp` / `rsync` over SSH | dev box ↔ laptop、スクリプト化に強い |
| Syncthing | 継続的バックグラウンド同期 (P2P、hosted なし) |
| iCloud Drive / Dropbox | **bundle がすでに暗号化済みなので OK** |
| USB / 外付け SSD | エアギャップ必要時 |
| メール添付 | 小さい bundle なら可能。暗号化済みなので原理的には安全 |

bundle は AEAD 済みの暗号文であり、可読な `.tar.gz` ではないため、バイトを保持する transport であれば何でも使えます。

## 推奨フロー

### 頻度が低い (月 1 回程度)

```sh
# laptop
export TRACEARY_BUNDLE_PASSPHRASE='your-passphrase'
traceary bundle export --out ~/Desktop/traceary-$(date +%Y%m%d).tbun

# AirDrop → desktop

# desktop
export TRACEARY_BUNDLE_PASSPHRASE='your-passphrase'
traceary bundle import --in ~/Downloads/traceary-*.tbun
```

### 頻度が高い (日次)

1. 2 台で Syncthing フォルダ (`~/.traceary/bundles/` 等) を共有する。
2. laptop 側で export の cron:
   ```cron
   0 19 * * * TRACEARY_BUNDLE_PASSPHRASE=... traceary bundle export --out ~/.traceary/bundles/$(date +%F).tbun --since $(date -v-1d +%Y-%m-%d)
   ```
3. もう一方で起動フック (または cron) で取り込む:
   ```sh
   for bundle in ~/.traceary/bundles/*.tbun; do
     traceary bundle import --in "$bundle"
   done
   ```

`bundle import` の既定は `--on-conflict skip` です。送信先にすでに存在する event / memory は skip され (`events_skipped` / `memories_skipped` にカウント)、同じ bundle を何度取り込んでも安全です。`--on-conflict replace` は bundle 側の row で上書きし、`--on-conflict error` は最初の UNIQUE 衝突で失敗して import 全体を rollback します。

Imported memory は candidate trust default を使います。新規 insert される row は、source machine で accepted だった場合でも常に `candidate` として保存されます。memory fact は accept 後に prompt context へ影響するため、別 machine からの import では既存の memory inbox review を必ず通します。既定の `skip` policy では送信先の既存 row は変更しないため、一度ローカルで review / accept した memory が re-import で candidate に戻ることはありません。

`--missing-parent {reject,skip,backfill}` も受け付けます。これは import する session の親 session が送信先に存在しない場合の方針を制御し、既定は `reject` です。Memory graph edges は代わりに `--orphan-edges {skip,reject}` を使います。既定は `skip` です。`memories.ndjson` import 後に endpoint が存在しない edge は skip され、`table=memory_edges`、`edge_id`、両 endpoint ID、endpoint 存在 boolean を含む structured warning を出します。`--orphan-edges=reject` は import を中止し、周囲の transaction を rollback します。

## Manifest v2 table registry spec

`manifest_version = 2` は `manifest.json.tables` を正とします。key は table name、value は次の形です。

```json
{
  "table_name": "memory_edges",
  "file": "memory_edges.ndjson",
  "row_count": 12,
  "checksum": "<NDJSON bytes の SHA-256>"
}
```

Import は write transaction を開く前に登録 file の checksum を検証し、未登録 payload file を拒否します。現在の five-table portability surface では、dependency order は次の通りです。

1. `sessions.ndjson`
2. `events.ndjson`
3. `command_audits.ndjson`
4. `memories.ndjson`
5. `memory_edges.ndjson`

### Five-table inclusion rules

| Table | 現在の writer | Import requirement |
|---|---:|---|
| `events` / `events.ndjson` | Included | 独立 row。`events.id` で冪等。 |
| `sessions` / `sessions.ndjson` | Included | 最初に import し event より先に所属 session を用意する。`--missing-parent` は親 session が送信先に無い場合の方針を制御。 |
| `command_audits` / `command_audits.ndjson` | Included | export 済み event に絞り込み。`event_id` で冪等。 |
| `memories` / `memories.ndjson` | Included | `memory_edges` より先に import。新規 row は既存でない限り `candidate` status。 |
| `memory_edges` / `memory_edges.ndjson` | Included | memories の後に import。両 endpoint が destination DB に存在する必要がある。既存 edge ID は既定 `--on-conflict=skip` で skip。 |

### Conflict matrix

| Condition | Default | Strict option | Transaction outcome |
|---|---|---|---|
| 既存 event ID | skip して `events_skipped` に count | `--on-conflict=error` | strict mode は rollback。 |
| 既存 session ID | skip して `sessions_skipped` に count | `--on-conflict=error` | strict mode は rollback。 |
| 既存 command-audit `event_id` | skip して `command_audits_skipped` に count | `--on-conflict=error` | strict mode は rollback。 |
| 既存 memory ID | skip して `memories_skipped` に count | `--on-conflict=error` | strict mode は rollback。 |
| 既存 memory edge ID | skip して `memory_edges_skipped` に count | `--on-conflict=error` | strict mode は rollback。 |
| 完全一致する既存 run lineage | skip して `run_lineages_skipped` に count | n/a | すべての conflict policy で冪等。 |
| 衝突・親欠落・循環のある run lineage、または不完全な usage link | reject | n/a | 常に全 table を rollback。`skip`、`replace`、missing-parent option では lineage 整合性を弱めない。 |
| import する session の親が missing | import を reject (`--missing-parent=reject`) | `--missing-parent=skip` / `backfill` | reject は rollback。`skip` は row を破棄、`backfill` は placeholder の親を補完。 |
| memories import 後も memory edge endpoint が missing | skip、`memory_edges_skipped` に count、structured warning を log | `--orphan-edges=reject` | strict mode は bundle import transaction 全体を rollback。 |
| bundle schema が local store より新しい | reject | n/a | write transaction は開始しない。 |
| manifest checksum / row-count mismatch | reject | n/a | write transaction は開始しない。 |

## スキーマ安全性

manifest には export 時の `schema_migrations` 最大バージョンが記録されます。`bundle import` は bundle が **ローカル store より新しい schema** で作られている場合は import せず、先に Traceary を upgrade するよう求めます。

**古い schema** で作られた bundle は普通に取り込めます — 各 event が書かれた当時必要だった migration の集合が揃っていれば十分で、新しい migration は不要です。

migration `000028` は additive です。v27 の usage row は run attribution が unknown のまま有効です。新しい lineage を書き込んだ store の downgrade は非対応で、古い binary を使う場合は migration 前の backup を復元してください。

## bundle がやらないこと

- **realtime replication (litestream 的なブロック同期) はしません**。別途評価。
- **公開 / 共有用 bundle はありません**。対称鍵なので全 reader が同じ passphrase を持つ必要があります。
- **Traceary 内蔵の transport はありません**。local-first の posture を保つため、hosted 要素を取り込む予定は今のところありません。

## Follow-up (v0.9 以降)

- 公開鍵モード (passphrase 共有なしで受信者 pubkey に暗号化) で、passphrase を共有せずに協力者へ bundle を送る。
