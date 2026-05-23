# Cockpit dogfood チェックリスト

[English](./cockpit-dogfood.md)

`traceary tui` / operator cockpit を変更するリリース前に、このチェックリストを使います。目的は、技術的には接続されていても実利用では迷う導線を検出することです。

## 自動カバレッジ

実行:

```sh
go test ./presentation/cli -run 'TestCockpitDogfood'
```

dogfood テストで確認する内容:

- Home の all-green 状態。
- Home の doctor failure 状態。
- Home の candidate memory 通知。
- Home の stale session warning。
- Home の new events と recent failures。
- `accept` ではなく `edit/distill` または `skip` が期待される曖昧な memory candidate。
- 代表的な terminal size: 80x24、120x32、160x40。
- keyboard path:
  - Home から Live と event detail 経由で最新 failure を探す。
  - 曖昧な memory の evidence を確認し、誤って accept しない。
  - Doctor を開き remediation command を見つける。
- `TRACEARY_LANG=ja` と 80x24 での Japanese cockpit smoke。

Golden snapshot は `presentation/cli/testdata/cockpit/` にあります。意図した cockpit 文言・layout 変更時だけ更新してください:

```sh
TRACEARY_UPDATE_GOLDEN=1 go test ./presentation/cli -run TestCockpitDogfoodGoldenSnapshots
```

## 手動 release-prep smoke

cockpit release の tag 前に、実 terminal で以下を確認します:

1. `traceary tui --reset-state`
   - Home が triage board として開くこと。
   - footer が global navigation と help を説明していること。
2. 80x24、120x32、160x40 に resize。
   - section bar が理解できること。
   - 最重要 card の `next:` action が command 暗記なしに分かること。
3. Home で `?` を押す。
   - action menu が Live / Doctor / Memory / Sessions を説明していること。
4. `2` で Live へ移動。
   - event row が scan しやすいこと。
   - event を選んで Enter、`Esc` で quit せず Live に戻ること。
5. `4` で Memory へ移動。
   - 曖昧または low-confidence な candidate を探す。
   - UI が edit/distill または skip を促し、弱い candidate は `a` 1 回で accept されないこと。
6. `3` で Doctor へ移動。
   - warning/failure に remediation command が inline 表示されること。
7. `5` で Sessions へ移動。
   - 専用 session UI 実装までは handoff/session command への導線が出ること。
8. `TRACEARY_LANG=ja traceary tui --reset-state` を実行。
   - shell / footer / help・action menu / Home labels / Memory review の判断補助が日本語で理解できること。
   - `traceary doctor --json` などの literal command は、copy できる英語 command 名のまま残ること。

## Release gate

以下が 1 つでも該当する場合は release tag を打たないでください:

- Main task が Home または `?` help から発見できない。
- 十分な context なしに memory candidate を誤 accept できる。
- mark-seen 後の new memory/event 状態が曖昧。
- Doctor failure に具体的な next action がない。
- 80x24 smoke で最高優先 card の primary action が隠れる。
