# Codex plugin

[English](./codex-plugin.md)

Traceary の Codex 向け plugin は `plugins/traceary/` にあり、Codex CLI 公式の `/plugins` flow に乗せて使えます。
slash command / session-history skill は、公式 flow で plugin を install した時点で自動配線されます。plugin hook には追加の安全確認があります。Codex は non-managed hook の現在の定義をユーザーが確認して trust するまで実行しません。install 後と、hook 定義が変わる plugin update 後に `/hooks` を開き、Traceary の entry を確認して trust してください。`traceary doctor --client codex` は Codex が判定した有効な trust 状態を検査し、untrusted・変更済み・無効な hook を警告します。

## Codex 公式 /plugins flow で入れる (primary)

1. 先に Traceary CLI を入れます（agent hook が `traceary` バイナリを呼ぶため）。

```sh
brew tap duck8823/traceary https://github.com/duck8823/traceary
brew install traceary
# または
GO111MODULE=on go install github.com/duck8823/traceary@latest
```

2. このリポジトリを取得します。リポジトリは `.agents/plugins/marketplace.json` にローカル marketplace を持ち、`plugins/traceary/` に plugin 本体を持っています。

```sh
git clone https://github.com/duck8823/traceary ~/src/traceary
```

3. リポジトリ内で Codex を起動し、同梱 marketplace を発見させます。

```sh
cd ~/src/traceary
codex
```

4. Codex 内で `/plugins` を開き、marketplace として `Traceary Plugins` を選び、`Traceary` plugin を install します。Codex が plugin を管理下の cache に展開し、manifest に記述された hook を発見します。

5. `/hooks` を開き、Traceary plugin の hook command を確認して、現在の定義を trust します。install だけでは plugin hook は trust されず、定義が変わると再確認が必要です。

6. 新しい thread を開いて確認します。

```sh
traceary doctor --client codex --json
```

## 公式 flow が自動で組み込むもの

- `SessionStart`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `Stop`（本文を含まない usage と turn 境界の transcript。session 終了ではない — #1170）, `PostToolUse` hook（`plugins/traceary/hooks.json` で宣言、manifest から参照） — **Codex 側の `plugin_hooks` feature が有効で、現在の定義が `/hooks` で trust されている場合に限る**。それ以外は下記 **Hook fallback (plugin_hooks が利用できない環境向け)** セクションを参照
- slash command: `/traceary:help`, `/traceary:doctor`
- 文脈 skill（1 job につき 1 skill。詳細は [skills](./skills.ja.md)）: `traceary-session-history` / `traceary-session-refine` / `traceary-memory-review` / `traceary-memory-remember`。いずれも Traceary CLI 経由。

## Hook trust 診断と legacy fallback

現在の Codex は `/hooks` で有効な hook 状態を示します。`trusted` な hook は実行され、`untrusted`・変更済み (`modified`)・無効な hook は実行されません。Traceary は Codex 内部の hash algorithm を複製せず、現在の定義との比較を Codex に委譲します。

診断手順:

```sh
traceary doctor --client codex --json        # 有効な plugin hook trust を検査
codex                                        # /hooks を開いて Traceary entry を確認
```

古い Codex build が plugin hook 状態を提示できない場合、doctor は pass とせず未確認として報告します。まず Codex を更新してください。plugin-managed hook を本当に読み込めない環境では、手動登録を互換 fallback として使えます。

```sh
traceary hooks install --client codex --upgrade --traceary-bin "$(command -v traceary)"
traceary doctor --client codex --json
```

fallback は `~/.codex/hooks.json` に Traceary 管理のエントリ (`traceary-session-start` / `traceary-prompt` / `traceary-usage` / `traceary-transcript` / `traceary-session-stop` / `traceary-audit`) を直接書き込みます。Traceary 以外のエントリは保持されます。

## 実行時の hook 境界表

hook manifest は Codex が呼び出し得る hook を記述しますが、個々の実行で
すべての境界が実際に発行された証拠ではありません。次の表は 2026-07-24 に
Codex CLI 0.145.0 で marker だけを返すローカルプローブを実行した結果です。
隔離した `traceary` recorder を `PATH` に置き、session history は読まず、
headless mode では通常の text 出力と `--json` の両方を確認しました。

| Codex mode | tool を使わない marker turn で確認した hook | final-turn source | `transcript_path` |
|---|---|---|---|
| Interactive `codex` | `SessionStart`, `UserPromptSubmit`, `Stop` | `Stop.last_assistant_message` | あり |
| Headless `codex exec` | `SessionStart`, `UserPromptSubmit`, `Stop` | `Stop.last_assistant_message` | あり |
| Ephemeral headless `codex exec --ephemeral` | `SessionStart`, `UserPromptSubmit`, `Stop` | `Stop.last_assistant_message` | なし |

tool を使わないプローブでは `PostToolUse`、compact、subagent の hook を
実行していないため、それらの発行は表から推測しません。ephemeral mode でも
transcript の取得に rollout file は不要です。確認した `Stop` payload が
final marker を inline で持ち、Traceary は通常の transcript hook からそれを
永続化します。non-ephemeral mode は `transcript_path` を含む場合がありますが、
Traceary が assistant text に使うのは inline の `last_assistant_message` です。
その path を開いて assistant text を探しません。

将来の host または中断された headless 実行で activity はあるのに実行時の
`Stop` が無い場合、doctor は `final_turn_not_observed` を返し、final-turn
coverage を partial として扱います。manifest を発行証拠にはせず、欠落を
埋めるために別 rollout や無関係な private history を走査しません。

## Capture gap の診断

`traceary doctor --client codex --project-dir <workspace> --json` は
`codex-capture` check を返します。`<workspace>` を event read と同じ
canonical repository identity に解決し、次の証跡を本文なしで照合します。

- 直近7日分の session start / prompt / tool / compact / Stop の完全な
  commit 済み event aggregate。同じ session 内で Stop と対応していない
  prompt turn の本文なし件数を含む
- 上記 Stop session と照合した finalized interactive Codex usage
  （identity 用 timestamp が意図的に7日窓外となる deterministic unavailable
  observation も含む）
- 対象 workspace に関連付けられる全 durable spool command / action record
  （record の古さは問わない）

各境界は `stored`、`delivery_pending`、`stored_and_delivery_pending`、
`not_observed` のいずれかです。
`prompt_turns` と `uncovered_final_turns` により、以前の成功 turn が、
後続 prompt の final boundary 欠落を隠さないようにします。
check には `surface`、`client`、Traceary version、plugin key、hook trust、
canonical workspace も記録されます。reason は次の固定値です。

| Reason | 意味 |
|---|---|
| `capture_observed` | recent な commit 済み証跡があり、対応必須の gap は見つからない |
| `hook_spool_backlog` | Codex から Traceary には到達したが、delivery が durable spool に残り未commit |
| `spool_projection_partial` | spool の distinct working directory 数が bounded diagnostic の解決上限を超えたため、backlog の確認または drain が必要 |
| `usage_missing_after_stop` | Stop は commit 済みだが finalized usage も pending usage delivery もない |
| `final_turn_not_observed` | Stop と対応していない prompt turn が1件以上ある、または Stop 由来 transcript なしで headless usage だけがある。final-turn coverage は partial |
| `no_recent_capture_evidence` | commit 済み証跡も pending 証跡もない。capture 成功とはせず warning |

進行中の session が `stop:not_observed` になること自体は正当ですが、doctor
はその状態を complete capture とは判定しません。response を1回完了して
diagnostic を再実行し、それでも境界が無ければ install 成功の推測ではなく
partial coverage の warning として扱います。diagnostic は session ID、
prompt / transcript 本文、tool の
input / output を表示しません。usage retry spool は既存の本文なし identity
field だけを保存します。それ以外の spool payload も、ローカル照合のために
command / action と allowlist 済み session / cwd metadata だけへ射影します。
spool working directory の canonical 化は1回につき distinct 64件を上限とし、
超過時は absence を成功扱いせず `spool_projection_partial` を返します。

local path alias で session / event が見つからない場合は、
`codex-capture` が表示した `workspace=` の値で CLI の read を再実行してください。
`list`、usage aggregate、この diagnostic は同じ
canonical workspace identity を使います。

## 検証済み usage の取得

trust 済みの対話型 Codex `Stop` ごとに、Traceary は `CODEX_HOME/sessions`（未指定時は `~/.codex/sessions`）配下の対応するローカル rollout JSONL を読みます。turn は `turn_context` で始まり、対応する `task_complete` または `turn_aborted` で終わります。turn 直前の最後の累積 `token_count.info.total_token_usage` を、終端境界時点の最後の累積 snapshot から差し引きます。途中 snapshot と compaction snapshot は以前の snapshot を置き換えるだけで、合算しません。token の各 dimension は独立して扱います。非負で報告された dimension（0 を含む）は既知のまま保存し、省略された dimension は unavailable のままにします。両方の snapshot で既知の dimension が減少した場合、または負の値がある場合は負の差分を作らず、turn 全体を集計対象外の `unavailable` observation として記録します。baseline や終端 snapshot の欠落、境界の曖昧さも同様です。snapshot のない終端があると直後の turn も利用量を帰属できないため `unavailable` になります。その直後の終端 snapshot は、さらに後続する turn の新しい baseline としてだけ使います。

`traceary session run -- codex exec --json ...` では、capture mode を `headless_stream` に固定します。子コマンド境界の `--` より前の option だけを判定し、`--` の後ろにある文字列 `--json` は誤って対象にしません。stdout は変更せず転送し、メモリに保持するのは `thread.started.thread_id` と終端 `turn.completed.usage` だけです。終端で 1 つ以上の非負 counter が報告されていれば partial observation として保存します。報告済み dimension（ゼロを含む）は known のままにし、省略された dimension は unavailable のままにします。負の counter は安全側に倒して拒否します。headless と rollout は、本文を含まない同じ portable な排他キー `(thread_id, turn ordinal)` を observation に保存します。直列化した SQLite transaction と unique partial index により、そのキーで additive にできる observation は 1 件だけです。後から到着した、import された、または同時に到着した別 source は excluded evidence として保持するため、到着順に関係なく二重加算しません。

- 終端 turn ごとに本文に依存しない決定的な observation ID を付けるため、hook の再送、retry、session の resume で token を二重加算しません。
- 既知の 0 は既知の 0 のまま保存します。Codex が省略した field は数値 0 ではなく `unavailable` です。
- 安定した Stop `event_id` があるのに usage record を読めない場合は、全 counter を明示的な unavailable とした集計対象外 observation を1件保存します。usage も安定 delivery ID もない場合は、本文から identity を作らず skip します。
- reader は一致した path の全 component で symlink を拒否し、open 後の通常 file が検査済み file と同一であることを確認し、総 read byte 数と JSONL line size に強制上限を適用します。
- usage hook の再試行 spool へ保存するのは `session_id` と検証済み `event_id` だけです。assistant text など Stop payload の他 field は spool 書き込み前に破棄します。

取得処理はローカルだけで完結します。rollout や usage record をネットワークサービスへ送信せず、請求額も推定しません。

### 二重記録の注意点

trust 済みの plugin hook と `~/.codex/hooks.json` の手動 entry が同時に有効だと、**session / prompt / transcript / audit の各 event が二重に記録されます**。doctor で古い手動経路を検出・削除してください:

```sh
traceary doctor --client codex --json
traceary doctor --fix --dry-run --client codex
traceary doctor --fix --client codex
```

doctor が cleanup を提示するのは、Codex が現在の Traceary plugin hook 定義を trusted と報告した場合だけです。名前付きの Traceary 管理エントリ (`traceary-session-start` / `traceary-prompt` / `traceary-usage` / `traceary-transcript` / `traceary-session-stop` / `traceary-audit`) だけを削除し、Traceary 以外の hook と top-level field は保持します。trust が未確認・untrusted・変更済み・無効な場合は手動 fallback を残します。

cleanup 後に `traceary doctor --client codex --json` を再実行し、登録経路が一つだけになっていることを確認してください。

## Memory activation strategy

Codex は v0.12 で Traceary の full host-native activation を備えた最初の host でした。v0.13.0 では同じ activation 契約を Claude / Gemini に拡張し、これらは二ファイル import-stub 戦略を採用しています。Codex は single-file target のままです。accepted memory は引き続き Traceary の SQLite store を source of truth とし、activation command は Codex memory target（既定は `~/.codex/memories/traceary.md`）へ Traceary 管理ブロックだけを書きます。

```sh
traceary memory admin activate --target codex --status
traceary memory admin activate --target codex --dry-run --diff
traceary memory admin activate --target codex --apply
traceary doctor --client codex --json
```

apply path は必要に応じて target directory/file を作成し、管理ブロック外の user-authored content を保持します。accepted memory set が変わっていなければ冪等に no-op となり、新しい marker version の管理ブロックは上書きしません。完全な安全契約は [host-native memory activation ADR](../architecture/host-native-memory-activation.ja.md) を、host 横断比較や `invalid` からの復旧は [durable memory ガイド](../memory/README.ja.md#ホスト別-activation-strategy)を参照してください。

## 更新

リポジトリを更新して、次回 Codex が `/plugins` をリフレッシュした時に新しいバージョンを取り込みます。

```sh
cd ~/src/traceary
git pull --ff-only
```

plugin を一度外して入れ直したい場合は `/plugins` 画面から再インストールできます。

## Doctor と smoke test

プライマリなランタイム確認:

```sh
traceary doctor --client codex --json
```

maintainer 向けの構造確認（plugin manifest や hook、marketplace を変更したとき）:

```sh
go run ./cmd/repo-tooling integrations verify
```

## 旧 install/uninstall の削除 (v0.14.0, v0.15.0)

`traceary integration codex install` は **v0.14.0** で削除されました (#920)。`traceary integration codex uninstall` は **v0.15.0** で削除されました (#957)。`traceary integration` コマンドツリー全体は **v0.25.0 で完全削除** されました (#1266)。呼び出しは unknown command として失敗します。新規 install / uninstall は上記の Codex 公式 `/plugins` flow を使ってください。

### 旧 install を残した環境向けの手動 cleanup

v0.14 以前の `traceary integration codex install` 経路で配置した状態が残っている場合、まず Codex 公式の `/plugins` flow で Traceary plugin を uninstall してください（リポジトリ内で `codex` を起動 → `/plugins` → `Traceary` を uninstall）。Traceary が手動で配置していた残存ファイルは、次の手順で取り除いてください。

```sh
# 旧 active plugin cache を削除
rm -rf ~/.codex/plugins/cache/local-traceary-plugins/traceary

# 旧 marketplace copy を削除
rm -rf ~/.agents/plugins/plugins/traceary

# ~/.agents/plugins/marketplace.json から、source path が "./plugins/traceary" の
# "traceary" entry を削除。この local marketplace が Traceary だけを含んでいた場合は、
# copy 削除後に marketplace.json ごと削除してもかまいません。
# ~/.codex/config.toml から [plugins."traceary@local-traceary-plugins"] テーブルを削除
# ~/.codex/hooks.json から名前付きエントリ "traceary-session-start" / "traceary-session-stop" /
# "traceary-prompt" / "traceary-audit" を削除。`[features].codex_hooks` フラグは他の hook workflow の
# ために残します。
```

cleanup 後は上記の公式 `/plugins` flow で install し直し、plugin lifecycle は Codex 自身に委ねてください。
