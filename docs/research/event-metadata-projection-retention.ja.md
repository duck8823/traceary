# 決定: `event_metadata_projection` は残す (#1686)

[English](./event-metadata-projection-retention.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1686

## 決定

`event_metadata_projection` を廃止しない。21 本のメタデータ読みを `events`
へ戻さない。

前提は「`events` が狭くなれば projection は不要」だった。#1743 は狭くする
側表を測り、`events.body` を抜かないと決めた。`events` は広い。2026-08-10
の live 比較は今も有効。

## stage 1 が遅くなる理由

同じ 13 メタデータ列、`ORDER BY created_at_norm DESC, id DESC`、
`LIMIT 5000`、best of 3。参照ストア 2026-08-10（`sqlite3`、
`PRAGMA query_only=1`、596,212 events、`events` 6.331 GiB）。引用のみ。
この変更では live store を開いていない。

| クエリ | Warm | Cold |
|---|---:|---:|
| projection | 0.012 s | 0.042 s |
| `events` メタデータ、join なし | 0.032 s（2.7×） | 0.633 s（15×） |
| `events` id only | 0.005 s | — |

id-only 対照で index はコストではない。広い行の fetch がコスト。covering
index では回収予定の ~0.47 GiB の中に収まらない（leading 列の順が 7 通り、
projection は `command_audits(exit_code, failed)` も非正規化している）。

#1743 scratch（512 × 8 KiB、live path 拒否）: codec 後の repetitive は
1.07×。overflow の entropy は 1.87× で側表は projection 並みだがファイルは
**増える**。抽出はしない。

## 容量と #1620

projection 一族の純減は約 0.47 GiB。#1620 の収支はこれなしで約 2.57 GiB
で閉じている。`list` / session lookup に 2.7× / 15× を払ってそれを回収
する取引は、この issue が再評価を求めたもので、成立しない。

## 残すもの

- 現行の `FROM event_metadata_projection` 読み
- projection 上の `legacy_source_hook`（参照ストアでは 0 行。互換列なので
  この issue では落とさない）
- projection を追従させる writer trigger

## 非目標

- テーブルや index の drop
- 狭くなった `events` に対する 21 クエリの再計測（狭い `events` はない）
- live store を開くこと
