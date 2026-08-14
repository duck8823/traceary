# 決定: lock 待ちと行作業を分ける (#1833)

[English](./search-projection-lock-vs-row-work.md)

**Status:** decided.

**Date:** 2026-08-15

**Issue:** #1833

## 決定

書き込み lock の **取得** と **保持** を別時計で測る。

`BEGIN IMMEDIATE` は lock-duration 窓の残りを使う。lock を持ったあとの時間だけが行作業。lock を取れない競合は `lock_duration_cap_exceeded` のままで除外しない。hold 予算を超えた source 書き込み 1 件は #1794 と同じく `class=row_work` で飛ばす。

`LockTime` は `ConfigHash` に入らない。inventory の `BeginTx` は変えない。

## N 回失敗したら除外しない理由

checkpoint は作業と同じ transaction で進む。writer 競合中のストアも遅い行も同じ checkpoint に止まる。回数で除外すると良いイベントを落とす。

## SQLITE_BUSY を信号にしない理由

`busy_timeout` は 1000 ms、`LockTime` は 250 ms。acquire の context が busy より先に切れるので、競合と遅い行が同じに見えていた。

## 結果

- 別接続が `BEGIN IMMEDIATE` を保持 → 取得失敗、除外なし。
- source 書き込み 1 件の hold 超過 → `row_work` 除外、checkpoint が進む。identity は失敗した plan から取り、新しい snapshot は読まない。
- どちらでもない → 以前と同じ catch-up。
