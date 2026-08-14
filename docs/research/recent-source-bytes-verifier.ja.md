# 決定: `recent_source_bytes` は検証する。trigger では持たない (#1819)

[English](./recent-source-bytes-verifier.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1819

## 決定

`traceary store search-projection status` で cache と実測を突き合わせる。
`AFTER INSERT` / `AFTER DELETE` trigger（option 1）には移さない。verifier は
cache を書き換えない。

`recent_source_bytes` は ApplyBatch の増減のまま。status が追加で出すもの:

- `recent_source_bytes` — 永続 cache
- `recent_source_bytes_measured` — `search_projection_state.generation_id` の
  `SUM(decoded_bytes)`
- `recent_source_bytes_delta` — cache − measured
- `recent_source_bytes_evidence` — SUM が走ったら `complete` / `sum`

delta が 0 でないことが、issue が「外から見えない」と書いた drift。status は
直さない。eviction は今までどおり cache を読む。

## option 1 を採らない理由

option 1 だけが壊れない形で、rebuild の最熱 write に singleton UPDATE が乗る。
issue は測ってから、と書いている。この drift を出せるリリース済み binary は
ない（#1817 が mid-rebuild の残りを reset 済み）。v0.36 は hot-path のない
verifier にする。

## なぜ `recent_bytes` ではないか

`recent_bytes` は **active** generation の SUM。rebuild 中の cache は
`generation_id`（incoming）に対して書かれる。この 2 つを比べると毎 rebuild が
誤報になる。verifier は ApplyBatch が増減するのと同じ generation を見る。

## 非目標

- trigger で持つカウンタ
- source→eviction 境界での reconcile-and-correct
- doctor check（2 GiB ストアは metadata-only で recent tier を SUM してはいけない）
- live store を開くこと
