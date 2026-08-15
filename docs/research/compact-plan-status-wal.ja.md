# compact plan のあとに status が残す WAL（#1848）

[English](./compact-plan-status-wal.md)

先に `store compact plan` を走らせたあと `store search-projection status` が 0 バイトの `-wal` と 32,768 バイトの `-shm` を残す理由の scratch 測定です。live store は対象外です。

## 決定

**説明して、open 経路は変えません。** 次の compact preflight は #1845 がこの stale pair を回収します。status に checkpoint や journal-mode 変更は足しません。

## scratch が示したこと（`TestCompactPlanThenStatusLeavesEmptyWALPair`）

本番と同じ WAL DSN（`journal_mode=WAL`）。sidecar サイズは `Stat` だけです。WAL モードのファイルを read-only で開くと、測ろうとしている pair と同じものができます。

| 手順 | `-wal` | `-shm` |
|---|---|---|
| `search-retire` のあと | なし（0） | なし（0） |
| `compact plan` のあと | なし（0） | なし（0） |
| retire だけのあと `status` | 0 | **32,768** |
| plan のあと `status` | 0 | **32,768** |

`compact plan` は pair を残しません（stale sidecar を掃除してから inspect します）。`status` は `sqliteDSN` で `journal_mode(WAL)` を付けます。その接続を閉じると空の WAL sidecar が残ります。この scratch では pair は plan 専用ではありません。

当初の CLI 順序（`log` → `search-retire` → `status` では pair なし、間に `compact plan` を入れると出る）も同じ sidecar です。plan が既存のものを消し、status が新しい空 pair を作り、次の compact preflight が見ます。

## 直さない理由

#1845 は 0 バイト WAL + 32 KiB SHM を stale として exclusive lease 下で消します。status は projection の帳簿読みです。毎回 DELETE モードや checkpoint を強制すると、害のない残りのために本番の WAL 契約を変えます。

## 約束

この pair は `status` の WAL モード sidecar であり、write でも compact plan の journal-mode 切替でもありません。
