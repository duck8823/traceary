# 定期メンテナンスタスク

[English](./scheduled-tasks.md)

Traceary のメンテナンス作業のうち、低頻度でバックグラウンド実行したいものをまとめる。host hook surface の再点検、upstream regression の監視、inbox digest の更新などが該当する。本ドキュメントは maintainer 向けの推奨セット。

下記タスクは **Claude Code 自身の scheduling** (`/schedule` skill — Claude Code のエージェント run を cron で起動) を前提にしている。GitHub Actions を推奨しない理由は runner に Anthropic API key を別途設定する必要があるため。Claude Code の scheduled agent はローカル credential を流用できる。

## 日次 host hook drift check

**目的.** host CLI (Claude Code, Codex, Gemini) は minor release 間で hook を追加・廃止することがある。`docs/hooks/host-coverage.md` のマトリクスは手書きなので、定期確認しないと silently に stale になる。

**動作.** 日次の Claude Code agent が:

1. 各 host の hook reference (Claude Code docs, Codex CLI バイナリ strings, Gemini CLI 同梱 docs) を fetch。
2. `docs/hooks/host-coverage.md` を読んで wired / available / unsupported のマトリクスを parse。
3. host 側の hook 一覧と diff。
4. 新規 hook が出現、または既存 hook が消失したら `tech-debt: host hook drift detected — <host> <date>` で issue を作成（body に diff）。
5. drift なしなら silent 終了 — 余計な issue は立てない。

**推奨スケジュール.** maintainer のローカルタイムで毎朝 06:00。active session と被らない時間帯を選ぶ。

**セットアップ.**

```text
/schedule create
  cron: 0 6 * * *
  prompt: |
    Traceary の docs/hooks/host-coverage.md を upstream host hook
    reference と照合してください。各 host (Claude Code, Codex CLI 0.x,
    Gemini CLI 0.36.x) について reference page もしくは同梱 docs を
    fetch し、wired / available / unsupported のマトリクスと比較。
    新規 hook の出現または既存 hook の消失が検出されたら
    `tech-debt: host hook drift detected — <host> YYYY-MM-DD` で
    duck8823/traceary に issue を作成 (body に diff)。drift なしの
    場合は何もせず silent 終了。
```

**検証 (no-op run).**

単発の手動 trigger で「issue 作成」または「silent 終了」どちらかが正常に動作すれば良い。同日の re-run で既存 open issue を重複作成しないよう、タイトル prefix で match させる前提。

## 運用ルール

- 報告が無い日は **silent 終了** とし、operator の inbox に空 run が溜まらないようにする。
- scheduled agent が立てる issue は `tech-debt:` prefix + 日付スタンプを必ず付与し triage しやすくする。
- Traceary store にアクセスする scheduled agent は operator の DB path (`~/.config/traceary/traceary.db`) を使う。非デフォルト install のときだけ `TRACEARY_DB_PATH` を設定する。

## 関連

- [Host hook coverage matrix](../hooks/host-coverage.ja.md) — 日次 drift check の対象。
- [Hook contract](../hooks/contract.ja.md) — host 別 capability tier。
