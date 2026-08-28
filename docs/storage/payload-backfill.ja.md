# ペイロード圧縮バックフィル（退役）

[English](payload-backfill.md)

> v0.48 で退役（#2264）。残っている `command_audits` テキスト列（と event 本文）
> の符号化は `store compact` が payload codec で行います。
> `store payload-backfill` は unknown command のままです（v0.35.0 / #1872 で削除）。

ライブストア向け `PayloadBackfillDatasource` は削除されました。migration 054 の
`payload_backfill_runs` テーブルは残ります（migration は append-only）が未使用です。
符号化は compaction の work copy 上で行われ、専用コマンド・フラグ・opt-in はありません。

`traceary store compact` の JSON はこのステップを `steps.audit_encode` として報告します。
先に backup を取ってください。すでに codec metadata を持つ行はそのままです。
