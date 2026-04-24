# マシン間ハンドオフ

[English](./cross-machine-handoff.md)

#567 の一部 · #572 を close。

Traceary は local-first かつ single-SQLite です。`traceary bundle export` / `bundle import` は v0.9.0 で導入された可搬プリミティブで、hosted 面を追加せずに laptop ↔ work desktop ↔ remote dev box 間で履歴を運ぶためのものです。

## bundle の中身

v0.9.0 bundle (manifest_version = 1):

- `manifest.json` — schema version、作成時刻、使ったフィルタ、各ファイルの SHA-256。
- `events.ndjson` — `--since` / `--until` / `--workspace` に一致するイベント (deterministic な出力のため `created_at` でソート済み)。

**v0.9.0 にはまだ入っていないもの**: sessions、command audits、durable memories、graph edges。本 doc 出荷時に新規 follow-up issue に回します。event が主要な履歴であり、残りのデータモデルは安定化の途上にあるため、MVP はまず events のみで出します。

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

`bundle import` は冪等です。送信先にすでに存在する event は skip され (`events_skipped` にカウント)、同じ bundle を何度取り込んでも安全です。

## スキーマ安全性

manifest には export 時の `schema_migrations` 最大バージョンが記録されます。`bundle import` は bundle が **ローカル store より新しい schema** で作られている場合は import せず、先に Traceary を upgrade するよう求めます。

**古い schema** で作られた bundle は普通に取り込めます — 各 event が書かれた当時必要だった migration の集合が揃っていれば十分で、新しい migration は不要です。

## bundle がやらないこと

- **realtime replication (litestream 的なブロック同期) はしません**。別途評価。
- **公開 / 共有用 bundle はありません**。対称鍵なので全 reader が同じ passphrase を持つ必要があります。
- **Traceary 内蔵の transport はありません**。local-first の posture を保つため、hosted 要素を取り込む予定は今のところありません。

## Follow-up (v0.9 以降)

- `bundle export` / `bundle import` を sessions、command audits、durable memories (既定 `candidate`)、graph edges にも拡張。本 doc 出荷時に follow-up issue を作成。
- 公開鍵モード (passphrase 共有なしで受信者 pubkey に暗号化) で、passphrase を共有せずに協力者へ bundle を送る。
