# ペイロード圧縮バックフィル（ライブストア）

[English](payload-backfill.md)

> v0.35.0 で削除（#1872）。符号化は `traceary store compact` の途中で行います。
> `store payload-backfill` は unknown command です。

`traceary store payload-backfill` は、既存の圧縮可能なペイロードテキスト列を、
すでに `payload_codec.go` に実装されているバージョン付き zstd codec 経由で
書き直します。対象は次のとおりです。

| テーブル | 列 |
|---|---|
| `events` | `body` |
| `command_audits` | `command_text`, `input_text`, `output_text` |

保持している履歴に圧縮を適用するオペレータ向けコマンドです。

これは **`store payload-rehearsal` ではありません**。rehearsal は *コピー* 上の
シャドウテーブルにだけ書き、insert を凍結し、ライブストアに非 identity
ペイロードがあると起動を拒否します。コスト予測用であり、本番の書き換えは
できません。backfill はライブストアをその場で更新し、writer を凍結せず、
identity / zstd が混在したコーパスをバッチ境界ごとに常に正当なストアとして扱います。

## いつ実行するか

- マイグレーション 053（codec 対応の body 由来トリガ）と 054（バックフィル用
  ブックキーピング）を含むビルドへアップグレードしたあと
- 圧縮済みイベント本文 **および** 圧縮済み command_audits テキストを前提にした
  #1620 のストアサイズゲート数値を使う前
- 検索 projection の rebuild と、その後の `store compact` を行えるメンテ窓

## 手順

1. ライブストアを **バックアップ** する（例: `traceary store backup create ~/traceary-pre-v0.34.db`）。
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
   無効化トリガが発火し projection は `drifted` / `stale` になります。audit テキスト
   の書き換えは projection 無効化を起こしません（migration 052 以降、検索 writer は
   それらを読まない）が、body が変わった場合は rebuild が必要です。`rebuild` という
   単一の verb はありません。新しい generation を start したあと、`status` が generation
   の `complete` を報告するまで `resume --until-complete` を繰り返してください。
   `stop_reason=max_batches` または `stop_reason=total_wall_time` ならまだ完了していないため、
   続けて `resume --until-complete` を実行します:

   ```sh
   traceary store search-projection start
   traceary store search-projection resume --until-complete
   traceary store search-projection status
   ```

7. ディスク上のファイルを縮めたい場合は **compact** する。エンコードは
   overflow ページを free list に返すだけで、物理バイトを返すのは
   `store compact` だけです:

   `store compact` 単体は help を表示するだけで何も圧縮しません。また `plan` は
   レガシー migration-032 index が残っている間は拒否します。これはこのバージョンで
   新規作成した store でも同じです（[#1847](https://github.com/duck8823/traceary/issues/1847)）。
   完全な手順は次のとおりです:

   ```sh
   traceary store search-retire
   traceary store compact plan          # run id が表示されます
   traceary store compact apply RUN_ID
   traceary store compact status RUN_ID
   ```

## オペレータが知るべき意味論

| 性質 | 挙動 |
|---|---|
| 選択条件 | フィールドごと: `<field>_codec IS NULL OR <field>_codec = 'identity'`。加えて codec 列 5 つが「すべて NULL」でも「すべて埋まっている」でもないフィールドも選ぶ（run を fail closed させるため） |
| レーン | `events.body`、続けて `command_audits.{command,input,output}_text`。共有の rowid カーソルが両テーブルを歩く。バッチは選択した各 rowid の全対象フィールドを読み、LIMIT で兄弟フィールドが取り残されないようにする |
| アフィニティ | zstd は BLOB、identity は TEXT で保存し、他の writer と揃える。plaintext を読む SQL（マイグレーション 053 の `LIKE`）が壊れない |
| カーソル | `events` と `command_audits` で共有する数値 `rowid` カーソル（各テーブルの rowid 列は独立）。イベント `id` / audit `event_id` は乱数なのでカーソルに使わない |
| ハイウォーター | 実行開始時にテーブルごとの天井を固定する: `high_water_rowid` = `MAX(events.rowid)`、`audit_high_water_rowid` = `MAX(command_audits.rowid)`。共有の max だと遅れている側の後続 insert が天井より下に入り、frontier が詰まったり未処理のまま completed になる。resume はチェックポイントから両天井を読み、再計算しない |
| Scanned rows | **レーン候補**の検査数（`events.body` 1 件、または `command_audits` の text フィールド 1 件）。物理行数ではない。監査行で 3 フィールドが対象なら 3 を加算する。JSON フィールド名 `scanned_rows` は契約上固定 |
| 不動点 | 書き換えも conflict skip もない完全走査になるまでパスを繰り返す |
| アトミックバッチ | ソース再検証・エンコード・フィールド + codec 列 5 つの書き込み・チェックポイント更新を 1 トランザクションで行う |
| 部分メタデータ | 件数を数え結果に id（event id / audit event_id）を載せ、run を fail closed。フィールドは書き換えない |
| 由来情報 | `body_stored_bytes` / `body_original_bytes` / `source_hook`・`legacy_source_hook`、および `input_original_bytes` / `output_original_bytes` は backfill が直接書かない。body 由来はマイグレーション 053 が表現変更をまたいで引き継ぐ |
| Projection | body 書き換えでは抑制しない。`drifted`/`stale` を前提に rebuild する |
| Recipe version | チェックポイントに記録。別 recipe の resume は拒否し、未書き込み prefix を飛ばさない。現行 recipe: `events-body-command-audits-zstd-v2` |

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
- 1 つの run を進められる worker は 1 つだけ。`resume` は新しい worker token を
  打ち直して run を掴み、チェックポイントは毎回その token と `state = 'running'`
  の両方を確認する。したがって 2 つ目の `resume` は先の worker を fence し、
  先の worker は次のバッチで「run was terminated by another worker」として中断する。
  カーソルやカウンタを同じ run に二重書きしない。失敗を記録できなかった場合も、
  行レベルの失敗ではなく同じ preemption として報告する。
- run の context をキャンセルした場合は `paused` チェックポイントを永続化してから
  戻る。バッチ間で気付いても、select やバッチトランザクションの中で気付いても同じ。
  `resume` で再開でき、`status` が実行中と誤報しない。complete / reset / pause /
  fail の終端遷移は、いずれも worker が既に確定させた作業の記録なので、
  キャンセルの届かない context で実行する。ただしその context には期限があり、
  store lease を取れないチェックポイントはハングせずに諦める。
- プロセスを落とした場合（CLI の `Ctrl-C`。現状 CLI は signal を command context に
  配線していない。#1747 を参照）はチェックポイントを書かない。実行中のバッチは
  ロールバックし、run は `running` のまま残る。`resume` は最後にコミットされた
  バッチから継続できるが、`status` が実行中と報告するため `run` はそれまで拒否される。
- キャンセルはエラーの名前を書き換えない。I/O・制約・デコードの失敗が `Ctrl-C` と
  競合した場合も、チェックポイントは残しつつ、報告するのは「cancelled」ではなく
  その失敗そのものである。

## 関連ドキュメント

- [ペイロード圧縮リハーサル](payload-rehearsal.ja.md) — コピー上でのコスト予測
- [検索 projection の rebuild](../search-projection-rebuild.ja.md)
- [安全な compaction](safe-compaction.ja.md)
