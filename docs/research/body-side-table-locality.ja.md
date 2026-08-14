# 決定: body の側表抽出は見合わない (#1743)

[English](./body-side-table-locality.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1743

## 決定

`events.body` を側表へ移さない。

overflow 行では locality の主張は正しく、scratch の側表は projection 並みの
メタデータ時間になる。それでもスキーマを変える価値はない。現行のメタデータ
読みはすでに `event_metadata_projection` で `events` を避けており、ファイルは
小さくならず、#1743 が列挙した mutation コストは払われない。

## 方法

`go run ./cmd/store-benchmark --measure-body-locality DIR` がコーパスごとに
scratch ストアを 2 つ作る（live path は使わない）:

1. **inline** — 本番の `EventDatasource.Save`（canonical codec。縮むときは zstd）
2. **side_table** — コピーしたあと `event_bodies(event_id, body)` へ移し、
   `UPDATE events SET body=''`、`VACUUM`

クエリは #1686 と同じメタデータ列（`ORDER BY created_at_norm DESC, id DESC`、
`LIMIT 5000` / `200`）と id-only 対照。ゲート（warm p50、`LIMIT 5000`）:

- method valid: inline / projection ≥ 2.0
- material: method valid、table scan 劣化なし、inline / side ≥ 2.0、かつ
  side / projection ≤ 1.5

seed `1743`。既定は 256 × 8 KiB。下表は 512 × 8 KiB、7 回、`modernc.org/sqlite`。

## scratch 数値（2026-08-15）

| コーパス | レイアウト | DB bytes | `events` pages (dbstat) | projection warm | events warm | 比 |
|---|---|---:|---:|---:|---:|---:|
| entropy | inline | 3.70 MiB | 2.10 MiB | 445 µs | 831 µs | 1.87× |
| entropy | side | 3.83 MiB | 0.11 MiB | — | 449 µs | projection 比 1.01× |
| repetitive | inline | 1.75 MiB | 0.14 MiB | 466 µs | 499 µs | 1.07× |
| repetitive | side | 1.79 MiB | 0.12 MiB | — | 457 µs | projection 比 0.98× |

plan は index scan のまま。id-only の covering scan はどちらのレイアウトでも
67–84 µs で、inline の追加コストは index ではなく table row。

entropy は 2× の method バーにわずかに届かない。repetitive は codec が
overflow を消しているので届かない。側表はどちらのコーパスでも resident
bytes を **増やす**（body は移しただけでファイルから消えない）。だから
harness の決定は `not_material`。

## live store（引用のみ。この計測では開いていない）

2026-08-10、参照ストア（`sqlite3`、`PRAGMA query_only=1`、596,212 events、
`events` 6.331 GiB、#1686 に記録）:

| クエリ | Warm | Cold |
|---|---:|---:|
| projection `LIMIT 5000` | 0.012 s | 0.042 s |
| `events` メタデータ `LIMIT 5000` | 0.032 s（2.7×） | 0.633 s（15×） |
| `events` id only | 0.005 s | — |

projection が残る理由はこれ。projection を残したまま body を抜く理由には
ならないし、#1743 の mutation 一覧を相殺しない。

## 払われないコスト

仮にゲートが material でも、抽出は次を設計しなければいけない:

- `ON DELETE CASCADE` / event-id 書き換え（migration 034）
- SQLite `CHECK` では書けない「body がある」不変条件
- `UPDATE OF body` トリガ 3 系統（038 / 039 / 041）
- 取りこぼすと静かに欠ける consumer 4 つ（canonical audit、archive segment、
  archive restore、bundle）
- `events.body_codec` に載る `legacy_index` 互換
- `command_audits` の locality（projection は exit/failed も非正規化している）

## 非目標

- `~/.config/traceary/traceary.db` を開くこと
- `event_bodies` の出荷や `events.body` の drop
- `event_metadata_projection` の廃止（#1686）
