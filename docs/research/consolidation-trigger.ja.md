# Consolidation トリガー（仕事量 + cadence）

[English](./consolidation-trigger.md)

Stop-hook の consolidation は、cover 境界以降に十分な **仕事量** がある **main** セッションへ、
**Stop cadence** で上限を付けて refinement を依頼します。body バイトはトリガーではありません（#2274）。

## 規則

```
enabled := min_commands > 0 && stop_cadence > 0
due     := enabled
        && isMainSession
        && covers_to 以降の command_executed >= min_commands   (既定 20)
        && (request が無い || 直近 request.at_event_id 以降の transcript >= stop_cadence)  (既定 8)
        && !stop_hook_active
```

- 仕事量は `COUNT(events.kind='command_executed')`。`command_audits` は join しない。body は読まない。
- ターンは `transcript` 行（記録された Stop につき 1 行）。
- cadence は依頼 **同士** の間隔。最初の依頼に窓は不要。
- サブエージェント（`parent_session_id` / `subagent_kind` / `agent` に `/`）は依頼しない。
- `consolidation.threshold_bytes` は非推奨。パースして無視し、明示時はプロセスあたり 1 回 `[WARN]`。

## 計測

最初の依頼の割合（cadence 無視）は 2026 年 8 月の main セッションに対する
`COUNT(command_executed)`。出荷済み SQL は
`infrastructure/sqlite/testdata/consolidation_replay.sql`。

## ロールバック

`config.json` に `"consolidation": {"min_commands": 0}`。再ビルド不要。
