# Gemini CLI extension

[English](./gemini-extension.md)

> **保守モードの案内:** Google は 2026-06-18 に無料版および Google AI Pro/Ultra 向け Gemini CLI の提供を終了し、**Antigravity** への移行を案内しています。Gemini Code Assist Standard/Enterprise、および有料の Gemini API キーか Gemini Enterprise Agent Platform API キーの利用者に対する Google 側の Gemini CLI サポートは継続します。このため、Traceary は該当環境向けに Gemini 拡張機能の提供と保守を続けますが、無料版・Pro・Ultra の新規利用者は [Antigravity プラグイン](./antigravity.ja.md) を導入してください。詳細は [Google の移行告知](https://developers.googleblog.com/an-important-update-transitioning-gemini-cli-to-antigravity-cli/) を参照してください。

Gemini 向け package は `integrations/gemini-extension/` にあります。Gemini CLI は install された extension の root に `gemini-extension.json` があることを前提にするため、Traceary では tagged release ごとにこの package を専用 archive として配布します。

## 自動で組み込むもの

- `SessionStart` / `SessionEnd` hook
- `BeforeAgent` prompt hook（送信された user prompt を `prompt` event として記録）
- `AfterAgent` transcript / usage availability hook（agent の応答を `transcript` event として記録し、本文を含まない hook から provider usage を取得できないことも明示的に記録）
- `run_shell_command` 向け `AfterTool` audit hook
- `PreCompress` compact marker hook（Gemini に post-compress summary hook がないため、圧縮前の境界だけを記録）
- slash command の `/traceary-help` と `/traceary-doctor`（`/traceary-help` は CLI・hooks・doctor の案内であり MCP 専用ではない）
- 文脈 skill（1 job につき 1 skill。詳細は [skills](./skills.ja.md)）: `traceary-session-history` / `traceary-session-refine` / `traceary-memory-review` / `traceary-memory-remember`。いずれも Traceary CLI 経由。

## Usage metadata

Gemini CLI では、次の 2 つの記録経路を意図的に分離します。

- `traceary session run -- gemini -p "..." --output-format stream-json` のように Traceary が起動した完結型コマンドでは、終端の `result.stats` を記録します。結果にモデル別合計がある場合はモデル別の行だけを保存し、全体合計を重ねて保存しません。
- 対話モードの `AfterAgent` hook では、usage を取得できないことを明示する観測を保存します。`AfterModel` はモデルへの request / response 本文も含むため、Traceary は usage 取得目的では導入しません。

adapter が読むのは、version 付きの本文を含まない metadata のうち、source identity・終了状態・timestamp・token 合計に必要なフィールドだけです。prompt / response の長さ、tool 回数、経過時間から usage を推測しません。同じ終端 result または `AfterAgent` timestamp を再配信しても二重計上しません。

## Memory activation strategy

Gemini integration は、Traceary の accepted memory store を instruction-file export・host-native activation 経由で利用できます。review 済み memory を Gemini instructions に見せる方法は次の 2 通りです。

**Option 1 — instruction-file export (引き続き利用可能)**: review 済み memory を `GEMINI.md` の Traceary 管理ブロックへ export します。

```sh
traceary memory admin export --target gemini --out GEMINI.md
```

**Option 2 — host-native activation (v0.13.0+、project 推奨)**: `traceary memory admin activate --target gemini` で `GEMINI.md` 内の小さな import stub と `.traceary/memories/gemini.md` の external memory file を管理します。activation pair は管理領域外の user-authored 内容を保持し、symlink / directory / malformed marker / newer marker 等の不安全 target を拒否し、idempotent です。Traceary は `save_memory` が生成する `## Gemini Added Memories` セクションを管理・書き換えません。そのセクションは Gemini auto-memory tool の所有物で、通常の host-context content として保持されます。セクションが既に存在する場合、Traceary は managed import stub をファイル末尾に append するため、両者は安全に共存します。Gemini 用 smoke test は `--apply` 後も seed した `## Gemini Added Memories` が byte-for-byte で保持されることを検査します。

```sh
# host pair を読み取り専用で確認
traceary memory admin activate --target gemini --status

# 計画の確認 (dry-run、書き込みなし)
traceary memory admin activate --target gemini --dry-run --diff

# 安全な per-file write で pair を反映（idempotent）
traceary memory admin activate --target gemini --apply
```

既定値:

- activation root: 直近の `.git` 祖先、なければ cwd
- host context file: `<root>/GEMINI.md`
- external memory file: `<root>/.traceary/memories/gemini.md`
- `GEMINI.md` に書き込む import 行: `@./.traceary/memories/gemini.md`

`--root <dir>` / `--path <file>` で上書き可能です。managed marker layout・status state・tracked-file policy などの全契約は v0.13 host-native memory activation [ADR](../architecture/host-native-memory-activation.ja.md) を参照してください。`invalid` からの復旧は [durable memory ガイド](../memory/README.ja.md#invalid-からの復旧)にまとめています。`traceary doctor --client gemini` には同じ dry-run / apply 再実行 command を持つ `gemini-memory-activation` check が surface されます。

## Install

### サポート対象の経路を選ぶ

- **無料版、Google AI Pro、Google AI Ultra:** Antigravity へ移行してください。Traceary CLI を導入した後、`traceary hooks install --client antigravity` で Traceary の hook を直接設定し、`traceary doctor --client antigravity` で確認します。[Antigravity ガイド](./antigravity.ja.md) では、別経路となる `agy plugin install` での packaged plugin 導入と、古い Gemini 形式パッケージの削除も説明しています。
- **Gemini Code Assist Standard/Enterprise、または有料の Gemini API キーか Gemini Enterprise Agent Platform API キー:** この Gemini 拡張機能を引き続き使用できます。Traceary では保守モードの連携として、互換性・不具合・セキュリティの修正を継続します。Google ホスト向けの新機能開発は Antigravity を対象にします。

Gemini 拡張機能をインストールしたままでも、その hook や設定は Antigravity へ自動移行されません。Antigravity 連携を別途設定して動作確認し、Gemini CLI が不要になってから Gemini 拡張機能を削除してください。

1. 先に Traceary CLI を入れます。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. Traceary の GitHub release から extension を install します。

```sh
gemini extensions install https://github.com/duck8823/traceary --ref <tag>
```

Traceary では、archive root が Gemini CLI の期待する extension root になる `traceary.tar.gz` asset を release ごとに公開します。

この repository を使ってローカル開発したい場合は、代わりに link を使います。

```sh
gemini extensions link integrations/gemini-extension
```

## Update

ローカル install した Traceary extension に `gemini extensions update` を使わないでください。対話プロンプトで止まり、headless ではハングします。実行中 CLI の tag に pin した install script を使います。

```sh
./scripts/install-gemini-extension.sh
# または明示的な ref:
./scripts/install-gemini-extension.sh --ref v0.43.0
```

Gemini CLI は同名が既に install 済みだと `extensions install` を拒否するため、スクリプトは既存 extension を退避してから uninstall → install します。各 `gemini` 呼び出しは `TRACEARY_GEMINI_TIMEOUT`（既定 60 秒）で打ち切ります。失敗や timeout では退避した copy を復元します。`--ref` がこの checkout の `VERSION` と一致する場合は temp clone ではなく `integrations/gemini-extension` から入れます（temp clone は Gemini の 2 段目 folder-trust でハングしたことがあります）。

uninstall-first の古い経路で host が空になったときの復旧:

```sh
gemini extensions install --consent integrations/gemini-extension
```

## Uninstall

```sh
gemini extensions uninstall traceary
```

## Doctor と smoke test

実運用の確認は次を基本にします。

```sh
traceary doctor --client gemini --json
```

`doctor` は Gemini capture について次の 3 つを確認します。

- `gemini-config`: Traceary 管理の hook が一部だけ（例: 旧式の
  SessionStart / SessionEnd / AfterTool のみ）になっている場合に警告します。
  settings.json へ入れている場合は `traceary doctor --client gemini --fix`
  で修復できます。
- `gemini-event-coverage`: recent Gemini session を見て、prompt/transcript が欠けた
  session 比率が `--coverage-threshold`（既定 `0.5`）を超えると警告します。audit のみの session も会話内容の coverage がないため警告対象です。
  settings.json ではなく Gemini extension package を使っている場合は、
  `./scripts/install-gemini-extension.sh` で CLI tag に合わせた
  BeforeAgent / AfterAgent hook を入れ直してください。
- `gemini-host-eligibility`: project config の検査を通過した場合に、bounded な
  headless `gemini -p` probe（使い捨て `TRACEARY_DB_PATH`、60 秒 timeout）を
  再実行し、アカウントが `IneligibleTierError` で拒否されると警告します。
  ineligible なアカウントでは host が `SessionStart` 直後に run を中断するため、
  hook が配線済みでも `prompt`/`transcript` event は記録されません。これは
  Google アカウント tier 側の拒否でありインストール不良ではなく、Traceary で
  修復できません（Antigravity への移行、または eligible なプランへの切り替えが
  必要です）。binary が無い・timeout・その他の理由で失敗した場合は pass ではなく
  skip を返します。

### 隔離 `-p` probe 手順（ineligible tier と hook 未配線の切り分け）

**アカウントの ineligible** と **hook の配線不良** を切り分けるには、Traceary
hook を導入済みの project から、使い捨て store への dogfood 観測を再実行します。

```sh
PROBE_DIR="$(mktemp -d)"
cd <Traceary hook を導入した project>
TRACEARY_DB_PATH="$PROBE_DIR/probe.db" gemini -p "Reply with the single word ok." --approval-mode plan
TRACEARY_DB_PATH="$PROBE_DIR/probe.db" traceary list events --limit 10
```

結果の見方:

- stderr に `IneligibleTierError`（"no longer supported for Gemini Code Assist
  for individuals"）が出て、使い捨て store に `session_started`/`session_ended`
  だけが記録される: **アカウントが ineligible** です。hook は正常で、host が
  `BeforeAgent` の前に run を中断しています。Antigravity へ移行するか、
  eligible な Gemini Code Assist / 有償 API プランへ切り替えてください。
- store に `prompt`（host が `AfterAgent` を発火すれば `transcript` も）が
  記録される: このホストでは配線も eligibility も正常です。
- `IneligibleTierError` がないのに `prompt` が記録されない: hook の配線不良として
  扱い、`gemini-config` / `gemini-event-coverage` を確認して管理 hook を更新
  してください（`traceary doctor --client gemini --fix`、または一致する
  release tag から extension を入れ直し）。

matrix の Gemini `prompt` セルは、この ineligible-tier の caveat 付きで `wired`
のままです。配線は健全で eligible なアカウントでは引き続き prompt を capture
しますが、セルはアカウント単位の capture を保証するものではありません。将来の
Gemini ビルドで `-p` が成功するようになった場合は、上記の手順による証拠が
matrix の probe 日付を更新する根拠になります。

package 自体の validate は次です。

```sh
gemini extensions validate integrations/gemini-extension
```

この repository からの end-to-end smoke test は次です。

```sh
TRACEARY_ENABLE_GEMINI_RUNTIME_SMOKE=1 ./scripts/smoke_test_integrations.sh gemini
```

この opt-in 環境変数は意図的です。Gemini CLI は browser 認証 prompt を開くことがあるため、既定の `./scripts/smoke_test_integrations.sh all` は headless な release-prep shell ではこの runtime probe を skip します。
