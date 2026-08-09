# ペイロード圧縮バックフィル（ライブストア）

[English](payload-backfill.md)

`traceary store payload-backfill` は、既存のすべての `events.body` を、すでに
`payload_codec.go` に実装されているバージョン付き zstd codec 経由で書き直します。
保持している履歴に圧縮を適用するオペレータ向けコマンドです。

これは **`store payload-rehearsal` ではありません**。rehearsal は *コピー* 上の
シャドウテーブルにだけ書き、insert を凍結し、ライブストアに非 identity
ペイロードがあると起動を拒否します。コスト予測用であり、本番の書き換えは
できません。backfill はライブストアをその場で更新し、writer を凍結せず、
identity / zstd が混在したコーパスをバッチ境界ごとに常に正当なストアとして扱います。

## いつ実行するか

- マイグレーション 053（codec 対応の body 由来トリガ）と 054（バックフィル用
  ブックキーピング）を含むビルドへアップグレードしたあと
- 圧縮済みイベント本文を前提にした #1620 のストアサイズゲート数値を使う前
- 検索 projection の rebuild と、その後の `store compact` を行えるメンテ窓

`command_audits` のテキスト列は **対象外** です（別チケットで追跡）。

## 手順

1. ライブストアを **バックアップ** する（例: `traceary store backup create`）。
2. 書き込みなしで対象量を **preview** する:

   ```sh
   traceary store payload-backfill preview
   ```

3. 書き換えを **run** する（再開可能・バッチ上限あり）:

   ```sh
   traceary store payload-backfill run
   # 任意のペース制御:
   traceary store payload-backfill run --batch-rows 256 --stop-after-batches 100
   ```

4. 中断または pause した場合は、同じバイナリの recipe で **resume** する:

   ```sh
   traceary store payload-backfill resume
   ```

5. 進捗確認:

   ```sh
   traceary store payload-backfill status
   ```

6. **検索 projection を rebuild する。** backfill は `events.body` を更新するため、
   無効化トリガが発火し projection は `drifted` / `stale` になります。これは正しい
   終端状態です。既存コマンドで rebuild してください:

   ```sh
   traceary store search-projection rebuild
   ```

7. ディスク上のファイルを縮めたい場合は **compact** する。エンコードは
   overflow ページを free list に返すだけで、物理バイトを返すのは
   `store compact` だけです:

   ```sh
   traceary store compact
   ```

## オペレータが知るべき意味論

| 性質 | 挙動 |
|---|---|
| 選択条件 | `body_codec IS NULL OR body_codec = 'identity'`。かつ codec 列 5 つがすべて NULL かすべて埋まっている行だけ |
| カーソル | `events.rowid`（単調）。イベント `id` は乱数なのでカーソルに使わない |
| ハイウォーター | 実行開始時の `max(rowid)`。実行中に取り込まれた行は identity のまま残り、次の実行対象になる |
| 不動点 | 書き換えも conflict skip もない完全走査になるまでパスを繰り返す |
| アトミックバッチ | ソース再検証・エンコード・body + codec 列 5 つの書き込み・チェックポイント更新を 1 トランザクションで行う |
| 部分メタデータ | 件数を数え結果に id を載せ、run を fail closed。行は書き換えない |
| 由来情報 | `body_stored_bytes` / `body_original_bytes` / `source_hook`・`legacy_source_hook` は backfill が直接書かない。マイグレーション 053 が表現変更をまたいで引き継ぐ |
| Projection | 抑制しない。`drifted`/`stale` を前提に rebuild する |
| Recipe version | チェックポイントに記録。別 recipe の resume は拒否し、未書き込み prefix を飛ばさない |

標準出力は集計カウンタだけの JSON です（本文は含みません）。

## コマンドが自動で確認する前提条件

- **counter モードの compatibility state**。*初版*の migration 36 を適用済みのストアは migration 043 に
  よって `legacy_index` モードのままになります。migration 036 の `payload_codec_events_update` trigger は
  `WHERE mode='counter' AND state='valid'` の行を更新し、該当が無ければ abort するため、そのようなストアでは
  `identity` → `zstd` の遷移が毎回 `constraint failed: invalid payload codec compatibility state` で
  バッチごと abort します。`preview` と `run` は開始前に拒否してモード名を提示し、開きっぱなしの run を残しません。
- **実行後は rehearsal が使えなくなります**。`store payload-rehearsal` は live store に non-identity payload が
  1 件でもあると開始を拒否します。rehearsal の証跡が必要なら、最初の backfill 実行の**前**に取得してください。

## 失敗と再開

- バッチ間のクラッシュは、正当なストアと `running`/`paused` チェックポイントを残す。
  同じ recipe version で resume する。
- 部分メタデータの行は run を fail closed し、イベント id を結果に載せる。
  修復してから新しい `run` を開始する（failed は resume 対象外）。
- resume は常にハイウォーター範囲の先頭からカーソルを再開し、mid-pass の pause で
  conflict skip された行が取り残されないようにする。

## 関連ドキュメント

- [ペイロード圧縮リハーサル](payload-rehearsal.ja.md) — コピー上でのコスト予測
- [検索 projection の rebuild](../search-projection-rebuild.ja.md)
- [安全な compaction](safe-compaction.ja.md)
