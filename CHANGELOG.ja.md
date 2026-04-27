# Changelog

[English](./CHANGELOG.md)

このファイルは、Traceary の各リリースで何が入ったかを時系列で追いやすくするための changelog です。  
release note と同じ粒度で、版ごとの要点だけをまとめています。

## [v0.11.1] - 2026-04-28

### Fixed
- **command audit 証跡を汎用 event surface から取得可能に (#842)** — `command_executed` の event body に command line / exit code / input payload / output payload を含め、`list` / `search` / MCP `list_events` / MCP `search` から Bash の検証証跡を取得できるようにしました。handoff の recent command summary は従来どおり command line のみに丸めます。
- **Claude compact summary から durable-memory candidate を生成 (#844)** — compact-summary event にも prompt / transcript / note と同じ heuristic extraction を適用し、日本語 label / durable marker (`決定`, `判断`, `制約`, `教訓`, `次回`, `確認済み` など) を認識するようにしました。Claude 風の日本語 summary が silent `[]` にならず、review-only candidate を生成します。
- **Codex の stale hook install を memory-capture gap として診断 (#843)** — `traceary doctor` が Codex `Stop` hook について `transcript` と `session stop` の両方を要求し、`UserPromptSubmit` / transcript capture の欠落が durable-memory extraction を starvation させることを明示します。修復導線は `traceary hooks install --client codex --upgrade` です。

### Changed
- **memory extraction の可視性判定を signal scoring 化 (#835)** — `extracted` / `extracted-hidden` の判定を、文字長だけでなく structured label、evidence ref、artifact ref、英日 durable marker、Latin/CJK 長さを組み合わせた score で行うようにしました。duplicate candidate は source 判定前に dedupe key ごとの最高 score を選ぶため、弱い先行 signal が強い structured evidence を隠しません。

## [v0.11.0] - 2026-04-27

### Added
- **lifecycle 観測ドキュメント (#817, #818)** — `docs/hooks/lifecycle-events.md` と `docs/hooks/host-coverage.md` (en+ja) を新設し、6 つの lifecycle event kind と host 別 wiring matrix を整理しました。
- **hook 駆動 L2/L3 memory pipeline (#825, #826, #829, #831)** — Claude `PreCompact` の summary を `sessions.summary` に sync し、subagent-stop / session-end hook が `source=extracted` / `status=candidate` の memory を auto-extract、`handoff` と `get_context` の payload に candidate を status marker 付きで含めるようになりました。
- **品質フィルタ + extracted-hidden 可視性 (#831, #833)** — auto-extract された candidate を文字長ヒューリスティック (Latin 20 / CJK 10 runes、artifact 系は除外) でフィルタし、新規 `extracted-hidden` source は store には残しつつデフォルトでは表示しません。CLI / MCP search ともデフォルトでは `extracted-hidden` を除外し、明示時のみ含めます。
- **14 日経過した stale candidate の自動 expire (#834)** — gc が 14 日以上前の auto-extract された candidate (`source IN ('extracted', 'extracted-hidden')`) を削除します。FK 制約を壊さないよう、`supersedes_memory_id` で参照している側を先に NULL 化します。
- **SKILL 再定義 (#821, #822)** — `traceary-memory-review` (review/inbox/recap 専用) と `traceary-memory-remember` (明示要請時の write 専用) を 3 ホストすべてに同梱し、旧 `traceary-memory-capture` を deprecate しました。
- **Gemini lifecycle wiring (#819, #820)** — Gemini CLI で `BeforeAgent` を `prompt` event 源に、`PreCompress` を `compact_summary` marker に配線しました。
- **日次 host hook drift check (#828)** — `docs/operations/scheduled-tasks.md` に、上流 host hook reference と `host-coverage.md` の日次 diff を `/schedule` で回す手順を記載しました。
- **memory モデル docs (#823, #824, #827)** — README を 3 層モデル表現に揃え、`candidate` status の表記を統一、`evidence_refs` / `artifact_refs` の kind enum を公開しました。

### Changed
- **`traceary top` の East Asian Wide 文字対応 (#836)** — `top` / `event_text_formatter` / timeline padding の column 計算を rune count から runewidth ベースに置き換えました。host locale の差で結果が変わらないよう、init 時に `runewidth.DefaultCondition.EastAsianWidth` を narrow に固定しています。
- **MCP body truncation (#837)** — `list_events` / `get_context` / `search` で event body を default 500 runes で truncate し、`body_truncated` / `body_length` marker と `body_limit` / `full_body=true` の override を追加しました。truncate 時には `body_blocks` を出さず、再取得は同じ呼び出しに `full_body=true` を付ける指示を schema に明記しています。

### Notes
- Codex CLI の `compact_summary` は upstream openai/codex#16098 が未着地のため v0.11.0 でも未対応です。
- v0.11.0 は minor release: schema 変更なし、JSON contract の追加のみです。

## [v0.10.3] - 2026-04-26

### Breaking changes
- **`traceary top --snapshot --json` contract の分離 (#795)** — top snapshot JSON は `traceary session tree --json` の contract 再利用をやめ、top 専用 contract に分離しました。既存の session tree field は維持しつつ、live dashboard consumer 向けに `latest_event_kind` / `latest_event_message` / `latest_event_at` を追加します。

### Added
- **`traceary top` 行に workspace と latest event を表示 (#794)** — text snapshot と live TUI の各行に `workspace=…/owner/repo`（末尾保持、36 runes で truncate）と `last=<kind>: <message>`（80 runes で scrub + truncate、event が無い session は `-`）を追加し、並列 session を一目で区別できるようにしました。interactive 推奨フロー doc でも `traceary top` を最初の live 確認コマンドとして紹介します。
- **`SessionSummary` に latest event metadata を追加 (#793)** — `list_sessions.sql` / `list_session_tree.sql` / `session_lineage.sql` に `latest_events` window function CTE を追加し、`LatestEventKind` / `LatestEventMessage` を 1 query で取得します。tie-breaker は `created_at DESC, id DESC`、新規 index `idx_events_session_created_at_id_desc` を追加しました。body は `ExtractPlainBody` を必ず通すため transcript の thinking block は message に到達しません。MCP `session_status` payload は新 field を露出しないことをテストで担保します。

### Performance
- **`list_sessions` を集計前に pagination する (#793)** — `LIMIT` / `OFFSET` を `filtered_sessions` CTE 内に push down し、event 集計が呼び出し側が要求した page 分の session にだけ走るようにしました。


## [v0.10.2] - 2026-04-26

### Fixed
- **Hook audit / subagent-stop test の時間爆弾** — `TestRootCLI_HookAuditCommand_UsesActiveSubagentSession` と `TestRootCLI_HookSubagentStopCommand_EndsChildAndClearsActiveState` の `started_at` がハードコードされており、24 時間 TTL (`hookActiveSubagentStateTTL`) を超えると `pruneHookActiveSubagentState` がアサート前に状態をクリアして失敗していました。v0.10.1 の Homebrew formula PR の CI で発覚。`time.Now().UTC()` を使う形に修正し、常に TTL 内に収まるようにしました。

## [v0.10.1] - 2026-04-26

### Fixed
- **`Agent` tool 名に対する PreToolUse subagent capture (#785)** — Claude Code の plugin hook が PreToolUse:subagent-start で `Task|Agent` を matcher に取るよう修正しました。以前は `Task` のみを受け付けていたため、現行の Claude Code が `Agent` という tool 名で dispatch する subagent 呼び出しが active-subagent state file も子 session 行も生成せずに通過していました。v0.10.0 リリース後の dogfooding (#778) で発見しました。

## [v0.10.0] - 2026-04-26

### Added
- **Bundle manifest v2 skeleton (#737)** — bundle export は `events` の table registry entry (`table_name`, file, row count, SHA-256 checksum) を含む `manifest_version=2` を書き出します。import は v1 reader path を維持し、`bundle import` に `--on-conflict {skip,replace,error}` と予約済みの `--missing-parent {reject,skip,backfill}` flag を追加しました。
- **Bundle durable memories (#739)** — bundle export は scope、validity、supersession、evidence refs、artifact refs を含む `memories.ndjson` を追加します。bundle import は `memories_imported` / `memories_skipped` を表示し、新規 import された memory は source が accepted でも review 必須の `candidate`（candidate trust default）として保存するため、cross-machine の memory trust が自動昇格しません。
- **Marketplace release manifest verification (#713)** — `scripts/verify_release_manifests.py` が Claude/Codex marketplace manifest の存在と、Claude / Gemini / Codex plugin manifest の version が root の `VERSION` と一致することを検証します。CI、release workflow、`make release/bump` が公開前にこの guard を実行します。
- **`traceary doctor` の sectioned output (#734)** — doctor report を `Environment` / `Database` / `Plugins` / `MCP` / `Hooks` に分類し、JSON に additive な `sections` / `summary` / `exit_code` を追加しました。check severity は `PASS` / `WARN` / `FAIL` に正規化し、終了コードは all pass = `0`、fail あり = `1`、fail なし warning あり = `2` です。
- **Experimental Anthropic native memory-tool backend (#742)** — `pkg/anthropicmemory` が Anthropic SDK の `BetaMemoryTool20250818CommandUnion` payload を受け取り、Traceary local の `memory_tool_files` table を backend にした SDK `tool_result` content を返す Go handler を公開します。tool contract は `memory_20250818` に pin し、upgrade は SDK field の自動 bump ではなく manual review 必須です。live API smoke 用 example は `examples/anthropic-memory/` にあります。

### Docs
- README と Claude plugin docs は、削除済みの `claude plugins install` CLI 表記ではなく Claude Code の `/plugin marketplace add` → `/plugin install` flow を案内します。
- `docs/release/README.{md,ja.md}` に release manifest verification と、version drift 時の `make release/bump VERSION=X.Y.Z` 修正手順を追加しました。
- `docs/integrations/anthropic-memory-tool.{md,ja.md}` — native memory tool と Traceary MCP `manage_memory` の使い分け、path traversal / size limit の threat model、SDK wiring、storage inspection / backup、experimental status、version pinning を説明します。`docs/integrations/agent-sdks.{md,ja.md}` は誤っていた Python lock-in 理由を削除し、native Go backend へ link します。

### Breaking changes
- **MCP tool 統合 (#708, breaking)** — MCP server が公開する tool はちょうど 8 個になりました。旧 24 tool 名は削除し、action parameter dispatch に移行しました:

  | 旧 tool | 新しい呼び出し |
  |---|---|
  | `propose_memory` | `manage_memory(action="propose", ...)` |
  | `remember_memory` | `manage_memory(action="remember", ...)` |
  | `accept_memory` | `manage_memory(action="accept", ids="<id>", ...)` |
  | `reject_memory` | `manage_memory(action="reject", ids="<id>")` |
  | `expire_memory` | `manage_memory(action="expire", ids="<id>", ...)` |
  | `supersede_memory` | `manage_memory(action="supersede", target_id="<id>", fact="...", ...)` |
  | `set_memory_validity` | `manage_memory(action="set_validity", ids="<id>", valid_from="...", valid_to="...", ...)` |
  | `import_memory_instructions` | `manage_memory(action="import_instructions", ...)` |
  | `accept_memories_batch` | `manage_memory(action="accept", ids=[...], ...)` |
  | `reject_memories_batch` | `manage_memory(action="reject", ids=[...])` |
  | `retrieve_memories` | `query_memory(action="retrieve", ...)` |
  | `export_memories` | `query_memory(action="export", ...)` |
  | `memory_pack` | `query_memory(action="pack", ...)` |
  | `scan_memory_hygiene` | `query_memory(action="scan_hygiene", ...)` |
  | `start_session` | `manage_session(action="start", ...)` |
  | `end_session` | `manage_session(action="end", ...)` |
  | `active_session` | `session_status(action="active", ...)` |
  | `latest_session` | `session_status(action="latest", ...)` |
  | `session_handoff` | `session_status(action="handoff", ...)` |
  | `add_log` | `record_event(type="log", ...)` |
  | `add_audit` | `record_event(type="audit", ...)` |
  | `list_events` | `list_events(...)` |
  | `search` | `search(...)` |
  | `get_context` | `get_context(...)` |

- **CLI JSON timestamp と duration の freeze (#729)** — golden fixture を記録する前段として、CLI `--json` のすべての timestamp フィールド (`created_at` / `started_at` / `ended_at` / `valid_from` / `valid_to` 等) を UTC RFC3339Nano (`YYYY-MM-DDTHH:MM:SS[.nnnnnnnnn]Z`) に統一。`traceary session tree --json` は `duration_ms` を削除し `duration_sec` のみを保持。`traceary timeline --json` は string `duration` を numeric `duration_sec` に置き換え。

## [v0.9.0] - 2026-04-25

マイナーリリース: **multi-host local memory substrate completion (v1.0 前の安定化)**。v0.9 では v1.0 スコーピング前の portability + 構造ギャップを埋めます。CLI のトップレベル整理、durable memory への additive な temporal graph overlay、暗号化済みクロスマシン event バンドル、agent SDK 統合 docs の整備が中心です。

### Added
- **Memory graph overlay (#573)** — additive な `memory_edges` テーブルで、既存 memory ストア上に型付き関係 (`supersedes` / `contradicts` / `supports` / `related-to` / `causes`) を重ねます。各 edge は自身の半開区間 `[valid_from, valid_to)` を持ち、`--as-of` クエリが memory validity と合成できます。`traceary memory graph add <from> --to <id> --relation <type>` と `traceary memory graph list [--memory-id <id>] [--relation <type>] [--as-of <ts>]`。migration 000013 + 複合 partial index 追加。SQLite が主ストアのまま、graph DB 依存はなし。完全な評価は `docs/architecture/temporal-memory.ja.md` を参照。
- **暗号化可搬バンドル (#572)** — `traceary bundle export --out <path>` が XChaCha20-Poly1305 アーカイブ (Argon2id で鍵導出) を出力。任意の transport (AirDrop / scp / Syncthing / iCloud — すでに AEAD 暗号化済みのため) で運べます。`traceary bundle import` は冪等 (UNIQUE 衝突は `events_skipped` カウント)。v0.9 では events のみ。sessions / audits / memories / edges は #702 に follow-up。
- **Agent SDK 統合 docs (#571 + #564-A)** — `docs/integrations/agent-sdks.{md,ja.md}` で Claude Agent SDK / OpenAI Agents SDK / Google ADK を検証済みの MCP 統合例とともに整理。Python コードは追加なし — `traceary mcp-server` が canonical path。Anthropic native memory-tool backend は #699 で v0.10 defer。

### Changed
- **CLI トップレベル再整理 (#696)** — 管理系を `store` 配下へ移動 (`store init`、`store backup create/restore`、`store gc`)、session ブートストラップ系を `session` 配下に集約 (`session handoff`、`compact-summary` を置き換える `session handoff --compact-only`)。トップレベル数 22 → 16。旧 top-level `init` / `backup` / `gc` / `handoff` / `compact-summary` は **deprecated alias** として動作し続けますが、deprecation 通知は **stderr** にだけ送られます (v0.8.x のスクリプトで stdout が byte-for-byte 互換)。`--help` のリストからは隠されます。alias は v1.0 で削除予定。

### Dependencies
- `github.com/pelletier/go-toml/v2` 2.2.4 → 2.3.0
- `golang.org/x/mod` 0.34.0 → 0.35.0
- `golang.org/x/sys` 0.42.0 → 0.43.0
- `modernc.org/sqlite` 1.48.2 → 1.49.1
- 新規: `golang.org/x/crypto` (Argon2id + XChaCha20-Poly1305 を bundle 暗号化に使用)

### Docs
- `docs/architecture/temporal-memory.{md,ja.md}` — temporal graph 評価 + 最小 overlay 設計。
- `docs/integrations/agent-sdks.{md,ja.md}` — SDK MCP 統合マトリクスと example。
- `docs/operations/cross-machine-handoff.{md,ja.md}` — bundle export/import フロー、transport 推奨、schema 安全ルール。
- CLI リファレンスで `session handoff` / `store *` を canonical 表記として案内、旧パスは deprecated として記録。

### v0.9.0 に含まれる作業項目
- #696 v0.9-5 CLI subcommand 再整理
- #573 v0.9-3 temporal knowledge graph 評価 + 最小 overlay
- #571 v0.9-1 OpenAI Agents SDK / Google ADK 統合評価
- #564 v0.9-4 Claude Agent SDK 統合 (MCP 経路。native memory-tool backend は #699 へ split)
- #572 v0.9-2 暗号化 bundle export / import (events)

### Follow-up
- #699 — v0.10 で Anthropic native memory-tool backend を再評価
- #702 — v0.10 で bundle を sessions / audits / memories / edges に拡張

## [v0.8.2] - 2026-04-24

v0.8.1 quality phase で見つかった tech-debt をまとめた patch リリース。transcript の search が thinking block 本文を漏らさなくなり、MCP \`list_events\` は plain-text projection に加えて canonical block 形も返すようになり、\`--source-hook\` フィルタは複合 index を使い、\`presentation/cli/doctor.go\` は \`infrastructure/filesystem\` を直接 import しなくなりました。

### Added
- **MCP \`list_events\` / \`add_log\` が \`body_blocks\` を返す** — canonical transcript envelope は構造化された \`[{type, text}, ...]\` として公開されるので、プログラム的コンシューマは transcript を round-trip したり独自の block-aware renderer を書けます。既存の \`body\` フィールドは plain-text projection (thinking は含まない) を返し続けます。\`search\` と \`get_context\` は意図的に \`body_blocks\` を省略します (これらのサーフェスで thinking block の内容が漏れないようにするため)。
- **source_hook 用 SQLite 複合 index** — migration 000012 が既存の \`idx_events_source_hook\` を落として \`idx_events_source_hook_time (source_hook, created_at DESC, id DESC) WHERE source_hook IS NOT NULL\` に置換。primary-only query と legacy-fallback UNION ALL query の分離と合わせて、\`traceary list --source-hook <name>\` が covering index scan を使うようになり、created_at 順スキャンを in-memory でフィルタしていた旧挙動が解消されました。

### Changed
- **\`application\` に plugin / hooks inspector の契約を追加** — \`application.PluginCacheInspector\` + \`PluginCacheStatus\`、\`application.ClaudePluginDetector\` + \`ClaudePluginDetection\`、および \`application.HooksInspector\` の \`ExtractManagedKeyFromEntry\` メソッド。\`presentation/cli/doctor.go\` と \`claude_plugin_detection.go\` は \`infrastructure/filesystem\` を直接 import する代わりにこれらの interface 経由で利用するようになり、v0.8.1 quality phase で指摘された hexagonal の依存方向違反を解消しました。

### Fixed
- **\`traceary search\` が thinking-only match をスキップ** — v0.8.1 で \`ExtractPlainBody\` は表示面から thinking block を除外していましたが、SQL search は生の envelope に対して \`body LIKE ?\` を走らせていました。結果、thinking block 内にだけマッチする検索は、表示が空の行を返し、内部推論を search 面に漏らす形になっていました。SQL は canonical envelope に対して \`json_each\` で text block だけを取り出した射影に対して \`LIKE\` を走らせるようにしました (\`typeof()\` guard が \`DecodeCanonicalEnvelope\` と同等の契約を保証します)。legacy plain-text row と非 envelope JSON は従来通り生で searchable のままです。
- **Gemini CLI smoke 警告** — \`scripts/smoke_test_integrations.sh gemini\` は \`~/.gemini/projects.json\` に \`{}\` を書いていました。Gemini CLI 0.38 は \`{"projects":{}}\` を期待しており、\`ProjectRegistry.getShortId\` が cleanup 中に throw するため、smoke 出力に \`Early cleanup failed\` / \`Tool output cleanup failed\` が 2 件ずつ出ていました。正しい shape を書けば smoke coverage を弱めずに警告を黙らせられます。

### v0.8.2 に含まれる作業項目
- #682 transcript JSON envelope 内の thinking block を search から除外
- #683 \`--source-hook\` フィルタで source_hook index が使われるように修正
- #684 MCP list_events / add_log に body_blocks 追加
- #685 doctor が infrastructure/filesystem を直接 import しないように refactor
- #536 gemini smoke cleanup warning を silence

## [v0.8.1] - 2026-04-24

**hook の出処記録、transcript の構造化、validity 精度** を中心にした patch リリース。新しい `events.source_hook` 列が各イベントを生成した host 側 hook を正確に記録し、transcript / prompt の body は thinking と text を分離した構造化 JSON ブロックとして保存され、SQLite の memory validity フィルタは小数秒を落とさなくなったので半開区間 `[valid_from, valid_to)` の境界が正しく動きます。

### Added
- **`events.source_hook`** — 各イベントに、それを生成した hook 識別子 (`stop`、`subagent_stop`、`pre_compact`、`post_compact`、`session_start`、`session_end`、`user_prompt_submit`、`post_tool_use`、`after_agent`、`after_tool`) が付きます。hook 以外の書き込み (`traceary log`、MCP `add_log`) は NULL のままです。`traceary list --source-hook <name>` と MCP `list_events` の `source_hook` パラメータで絞り込め、`traceary show`、`list --wide`、`list --json`、`list --fields source_hook`、`context`、replay HTML すべてで表示されます。v0.8.0 以前の `[phase:subagent]` / `[phase:pre-compact]` body prefix row も、移行期間の fallback で同じ名前のフィルタに引っかかります。
- **transcript / prompt body の block 化** — 補助アシスタント transcript とユーザー prompt は `{"blocks":[{"type":"thinking","text":"..."},{"type":"text","text":"..."}]}` という JSON envelope で保存されるようになりました。block-aware なコンシューマは内部推論とユーザー向け回答を分離できます。legacy のプレーン文字列 row はそのまま round-trip します。envelope 形ではない任意の JSON body (例: `{"foo":"bar"}` の note) も手を加えずに保持されます。
- **`memory supersede --from / --to`** — CLI と MCP `supersede_memory` が置換先 memory の validity 境界を明示できます。`valid_to < valid_from` は `set-validity` と同じロジックで拒否します。
- **plugin cache multi-version warning** — `doctor` の stale チェックに、Claude plugin cache に複数バージョンの Traceary が共存しているとき (resumed session で `claude plugins update` を走らせた直後に起きがち) の警告を併合しました。fresh session で起動し直すガイダンスを返します。

### Changed
- **memory validity timestamp の保存形式** — `valid_from` / `valid_to` は固定長 9 桁 nano に正規化され、SQLite の辞書順比較が `time.Time` の比較と完全に一致するようになりました。v0.8.1 より前の row は migration 000010 で一度だけ書き換えます。validity フィルタは `datetime()` ラップを外したので partial index `idx_memories_valid_window` が実際に使われます。
- **`MemoryUsecase.Supersede` が validity を継承** — `memory hygiene apply` の supersede 遷移では、caller が明示的に上書きしない限り、置換先 memory が元の `[valid_from, valid_to)` 窓を引き継ぐようになりました。以前は捨てていて、`validity_overlap_supersede` が自己矛盾を作っていました。
- **hook body-prefix marker を write 側で廃止** — `hook_runtime` は `session_ended` に `[phase:subagent]` を、`compact_summary` に `[phase:pre-compact]` を付けなくなりました。代わりに `source_hook` が lifecycle を識別します。reader は v0.8.1 より前の row のために prefix fallback を維持します。

### Fixed
- **非 canonical な `--traceary-bin` basename** — `hooks install --upgrade` と `doctor` が、パッケージ済みバイナリが非 canonical なパス / basename (symlink、`/usr/local/bin/traceary-dev`、Homebrew cellar パス) にあるときでも Traceary-managed なコマンドを正しく認識します。以前の build は「追加」「削除」summary の誤報を出していました。

### Docs
- CHANGELOG 本ファイル (en / ja).

### v0.8.1 に含まれる作業項目
- #662 transcript / prompt body の JSON block 構造化
- #664 SQLite memory validity フィルタの小数秒脱落
- #665 `MemoryUsecase.Supersede` が valid_from / valid_to を伝播しない
- #666 Log redaction の統合
- #667 非 canonical な `--traceary-bin` basename の検出
- #670 doctor plugin-cache の active-snapshot / cache 不一致検出
- #672 events.source_hook 列 (書き込み側)
- #679 source_hook 読み取り側 — CLI / MCP filter + JSON field + replay HTML + body prefix marker retire

## [v0.8.0] - 2026-04-22

**replay UX / 時間的 memory / transcript 取得** を中心にしたマイナー: オペレーターが共有できる self-contained な replay HTML、memory ごとの有効期間窓、Claude Code / Codex CLI / Gemini CLI 全部の assistant 応答 transcript 取得。加えて Claude Code の 2026-Q2 hook (SubagentStop / PreCompact) を wire し、`traceary hooks install` / `doctor` の UX を締め直しています。

### Added
- **Replay export** — `traceary replay --out <path>` で inline CSS・外部アセットなしの self-contained HTML を 1 ファイルで書き出します。4 パネル (Sessions / Timeline blocks / Failure hotspots / Durable memories) と generated-at footer 構成。`--sessions` / `--events-per-session` / `--memories` / `--timeline-blocks` / `--hotspots` で各パネルの件数制御。timeline と hotspot は `traceary timeline` / `traceary list --failures-only` と同一意味なので相互参照できます。
- **transcript event** — 最終 assistant 応答を記録する新 event kind。Claude Code `Stop` (JSONL `transcript_path`) / Codex CLI `Stop` (`last_assistant_message`) / Gemini CLI `AfterAgent` (`prompt_response`) の 3 hook で発火。本文には built-in redactor とオペレーター設定の `redact.extra_patterns` が適用されます。
- **Memory の時間的有効期間** — accepted memory は `[valid_from, valid_to)` の half-open window を保持。`traceary memory set-validity --from / --to / --clear-to` で窓を設定、`traceary memory list --as-of YYYY-MM-DD` や MCP `session_handoff` / `memory_pack` が窓でフィルタするため任意時点へ time-travel した retrieval ができます。
- **Memory retrieval preset** — `traceary memory list --preset resume | review | incident` と MCP `session_handoff` / `memory_pack` に built-in の type / confidence / limit preset。`read.presets` のユーザー定義で同名を上書き可能。明示的な filter フラグは preset より優先。
- **Memory hygiene detector** — `memory hygiene scan` に `validity_overlap_supersede` を追加 (validity 窓を annotation した pair が重なるケース)。半開区間で disjoint な pair は historical fact として汎用 `supersede_candidate` からも除外されます。
- **Claude Code SubagentStop + PreCompact hook** — `traceary hook subagent-stop claude` と `traceary hook compact claude pre-compact` で wire。SubagentStop は `session_ended` + `[phase:subagent]` prefix、PreCompact は `compact_summary` + `[phase:pre-compact]` prefix として記録。`loadCompactSummary` が prefix を skip するので handoff / memory_pack は引き続き post-compact summary を返します。
- **PostToolUse matcher 拡張** — Claude hook install が `Bash` / `mcp__.*` に加えて `Read | NotebookRead | Edit | MultiEdit | Write | NotebookEdit | Grep | Glob | Agent | Task | TodoWrite | WebFetch | WebSearch | ExitPlanMode` を監査対象に。Claude Code 組み込みツールの利用をすべて audit できます。
- **`hooks install --upgrade`** — 非破壊マイグレーション。Traceary 管理分だけ置換、ユーザー追加分は保持、旧 event entry は strip、追加 / 更新 / 保持 / 削除の内訳を印字。`--force` とは排他。
- **`hooks install --matcher <preset>`** — `PostToolUse` の matcher preset `minimal` (`Bash` + `mcp__.*`)、`default` (+ v0.8-6 で追加された組み込みツール一覧。packaged install と同じ)、`all` (+ `.*`) の 3 つ。`--matcher` を省略すると `default`。
- **doctor の新 check** — `claude-plugin-cache` (cached plugin version vs marketplace manifest を semver 比較、古い場合は `claude plugins update` を案内) と `<client>-host-capabilities` (2026-Q2 のホスト機能を informational に表示)。
- **MCP 追加分** — `add_log` に redaction (built-in + `extra_redact_patterns`) が適用、`session_handoff` / `memory_pack` に `as_of` (時間 travel)、`retrieve_memories` が retrieval preset 対応。

### Changed
- **Read-only usecase を application 層に** — `ContextUsecase` / `ReplayUsecase` が `queryservice.*` を直接消費する設計になり、CLI / MCP は共通 builder を使用。Presentation 層で zero-value usecase を構築する fallback は削除。
- **Event body phase marker 共通化** — `domain/types/event_body_markers.go` に `EventBodyMarkerCompactPreSnapshot` / `EventBodyMarkerSubagentStop` を一元定義。kind を増やさず body prefix で phase を表現する方針なので下流消費は backwards-compatible。
- **Redaction leaf package** — `application/redaction` を write-side usecase に依存しない leaf package に整理。`transcript` / `add_log` 取り込みと `traceary log` / `hook audit` で同じ redactor 集合を共有。
- **Codex Stop の実行順序** — packaged Codex hook は `hook transcript codex` を先に、`hook session codex stop` を後に実行 (session-stop が workspace state file を clear するため、transcript は先に発火させて persisted workspace を使う)。

### Fixed
- **transcript redaction parity** — `hook_runtime.go` と MCP `add_log` が `extra_redact_patterns` を適用するようになり、audit path と同等のポリシーに。以前は transcript 本文だけ組織固有 secret shape が漏れていました。
- **CLI と MCP の null shape** — JSON 出力で CLI が `null` / MCP が `""` (あるいは逆) で食い違っていたフィールドを統一。空文字は省略、明示的 null は "未設定" 意味に固定。
- **`doctor` の pseudo-version semver 比較** — Go の pseudo-version (`v0.7.3-0.YYYYMMDDhhmmss-abc...`) を最新 release tag と semver 比較。dev build は "newer than the latest release" と報告されるようになりました。
- **`doctor` の plugin cache 古さ検出** — cached plugin manifest と marketplace manifest を読んで、cached が古ければ warn + `claude plugins update` を案内。
- **Hooks merge で `--matcher` だけ変更された pair** — managed key 比較に matcher も含めるようになり、preset 変更 (同 event・別 matcher) は `Refreshed` として扱われます。

### Docs
- `docs/hooks/contract{,.ja}.md` の三層 capability 表が transcript の新挙動と 2026-Q2 host-capability 付録を反映 (SubagentStop / PreCompact / Codex `last_assistant_message` / Gemini `AfterAgent` がすべて `wire 済み`)。
- `docs/integrations/codex-plugin{,.ja}.md` と `docs/integrations/gemini-extension{,.ja}.md` の「自動で組み込むもの」セクションに transcript hook を列挙。
- `docs/cli/README{,.ja}.md` に `--upgrade`, `--matcher`, `--timeline-blocks`, `--hotspots`, `--as-of`, `--preset` を該当コマンド配下で記載。

### Included work items for v0.8.0
- #566 v0.8-4 memory block 構造の評価
- #565 v0.8-3 temporal validity
- #606 v0.8-7 Claude Stop で transcript 取得
- #605 v0.8-6 PostToolUse matcher 拡張
- #570 v0.8-5 memory retrieval preset
- #563 v0.8-1 replay export
- #624 v0.8 quality-phase polish
- #625 v0.8-followup redaction leaf package 化
- #619 v0.8-followup transcript → memory extraction + MCP 説明
- #629 v0.8-followup SetValidity の CLI/MCP テスト
- #635 v0.8-followup propose_memory SKILL.md の誘導
- #640 v0.8-followup transcript kind の整合
- #633 v0.8-followup doctor plugin cache version check + upgrade docs
- #634 v0.8-followup doctor pseudo-version の semver 比較
- #626 v0.8-followup transcript extra_redact_patterns
- #628 v0.8-followup CLI / MCP の JSON null shape
- #617 v0.8-followup as_of ContextPackCriteria
- #648 v0.8-followup MCP add_log redaction
- #621 v0.8-followup PostToolUse matcher 2026-Q2
- #632 v0.8-followup hooks install `--matcher` preset
- #654 v0.8-followup monitoring UX agent を default に
- #637 v0.8-followup hooks install `--upgrade` flag
- #616 v0.8-followup auto-supersede heuristic (validity_overlap_supersede)
- #627 v0.8-followup replay bundle を application 層へ
- #636 v0.8-followup SubagentStop + PreCompact hook
- #630 v0.8-followup replay timeline + failure-hotspot panel
- #631 v0.8-followup transcript Codex / Gemini parity
- #666 v0.8.0 Log の redaction を EventUsecase 側に集約 (AuditRedaction と対称化)
- #667 v0.8.0 非 canonical な `--traceary-bin` basename を name prefix で認識

## [v0.7.2] - 2026-04-20

v0.7.1 の実運用で判明した tail polling の SQLITE_BUSY、audit 二重記録、hook install UX の問題を一括で潰す operational-safety hotfix です。

### 追加
- `traceary hooks install --global` で user-level config (`~/.claude/settings.json` / `~/.gemini/settings.json`) へ書き込めるようにしました。Codex は元から user-level なため `--global` は no-op 通知を出して続行します。`--global` は `--output` と排他で、HOME が相対パスの場合は拒否します。
- `traceary doctor` に `<client>-global-config` check を追加 (Claude / Gemini)。従来の project-level `<client>-config` と併せて両 scope の状態が一度に確認できます (Codex は既に user-level なため対象外)。

### 修正
- SQLite DSN に `journal_mode=WAL` / `synchronous=NORMAL` / `busy_timeout=5000` を有効化し、`traceary tail` の poll が短命な hook write にブロックされない / 一時的なロック競合は自動で retry される構成にしました。
- `traceary hooks install --client claude` は `~/.claude/settings.json` の `enabledPlugins` を参照して Traceary Claude Code plugin が有効な状態では install を skip し通知します。plugin + settings.json の二重登録で audit が 2 回記録される問題を解消。`--force` で plugin 開発時の両方登録を明示許可できます。
- `traceary doctor --client claude` は plugin + settings の二重登録を `warn` として報告し、plugin 有効時でも project 側の settings が破損していれば `fail` を維持します (従来は silent pass でした)。

### ドキュメント
- `docs/hooks/README{,.ja}.md` に `--global` / plugin-skip 挙動 / 追加された doctor check を記載。
- `docs/operations/README{,.ja}.md` に新 SQLite pragma と WAL sidecar の扱いを追記。
- `docs/backup/README{,.ja}.md` に live DB を sidecar (`<db>-wal` / `<db>-shm`) なしでコピーした場合の注意を追加。
- `docs/integrations/claude-plugin{,.ja}.md` に plugin 導入時は `hooks install` 不要である旨を記載。
- `docs/cli/README{,.ja}.md` の hooks install セクションに `--global` を追加。

### 対応 issue
- #607 v0.7.2-1 SQLite DSN WAL + busy_timeout
- #603 v0.7.2-2 hooks install / doctor plugin-aware dedup
- #604 v0.7.2-3 hooks install --global + doctor global-config check

## [v0.7.1] - 2026-04-20

v0.7 の Multi-AI レビューと v0.7.0 リリース workflow で洗い出された穴を塞ぐ patch リリースです。

### 追加
- `traceary memory hygiene scan --similarity <N>` に新 suggestion kind `supersede_candidate` を追加。同 scope で fact text は異なるが単語 Jaccard 類似度が閾値 (既定 0.6) 以上のペアを検出し、古い方を supersede 対象、新しい方の fact を置換テキストとして提案 (`replacement_memory_id` / `replacement_fact` / `similarity`)。`memory hygiene apply --ids` は `MemoryUsecase.Supersede` 経由で scope / type / refs を継承しつつ置換を適用。
- MCP `export_memories` (read-only) を追加。accepted memory を CLAUDE.md / AGENTS.md / GEMINI.md 形式の markdown として返し、agent host からファイルシステム経由でなくとも bridge を扱えるように。MCP `import_memory_instructions` は path / inline markdown のいずれかを受け取り candidate を propose する。

### 変更
- Bridge marker parser が `:v<N>` 形式の任意バージョンを regex でマッチするようになり、binary の既知最大版を超える `:v<N>` は警告を出す。exporter は引き続き `:v1` を書き出し、CLI の merge helper は新しいブロックを古いバイナリで上書きできないよう refuse するので、将来の `:v2` コンテンツが静かに失われることはない。
- Release workflow の umbrella auto-close matcher が、push された tag が `vX.Y.0` 形式のときに minor-version の title prefix (`v0.7: ...`) も受理するように拡張。将来の minor リリースで umbrella が自動 close される。

### 修正
- release workflow の `git tag` validation が `${{ inputs.tag }}` を shell へ直接展開していた GitHub Actions injection 形状を env 経由に変更。
- `mergeMemoryExportIntoExistingFile` の marker regex に行頭アンカーを追加し、prose (例: 手書きドキュメント内で marker 文字列に言及しているケース) が managed block として誤認されないように。
- `import_memory_instructions` MCP tool の annotation を `DestructiveHint: false` に修正 (`propose_memory` と同様、candidate を追加する additive write)。

### 対応 issue
- #592 v0.7.1-1 release workflow matcher
- #593 v0.7.1-2 similarity-based supersede suggestion
- #594 v0.7.1-3 MCP export / import instructions tools
- #595 v0.7.1-4 bridge marker `:v<N>` forward-compat parsing

## [v0.7.0] - 2026-04-20

Codex 標準化 + durable-memory governance リリースです。read UX (configurable columns / preset / color highlight / session follow) を進めつつ、Codex と subagent lineage の一級 capture を追加し、durable memory を inbox review / host ファイル双方向 bridge / hygiene ツールを備えた governed substrate に昇格させました。

### 追加
- `traceary memory import codex` で `~/.codex/memories/MEMORY.md` の User preferences / Reusable knowledge / Failures セクションを file evidence ref 付きの candidate として取り込み
- `traceary memory inbox list / accept --ids / reject --ids` と MCP `accept_memories_batch` / `reject_memories_batch` で candidate レビューを完結
- `traceary memory export --target <claude|codex|gemini> --out <path>` で accepted memory を `<!-- traceary-memories:begin:v1 -->` マーカー付き markdown として deterministic に書き出し、既存の手書きセクションは保持したまま管理ブロックのみを置換
- `traceary memory import instructions --source <...> --in <path>` で CLAUDE.md / AGENTS.md / GEMINI.md からの候補取り込みをサポート (marker 内は重複回避のため skip)
- `traceary memory hygiene scan` が `redaction_hit` / `expiry_candidate` / `duplicate` を提案、`memory hygiene apply --ids` で該当 transition を commit、MCP `scan_memory_hygiene` が読み取り専用で mirror
- `traceary tail --follow-session <prefix>` で特定 session の tailing (最低 8 rune、UUID 対応)
- `traceary session tree` JSON に `parent_session_id` / `depth` / `duration_ms` / `subagent_type` を追加、text 行に subagent role と `N cmds / M events`、`--root <session-id>` で subtree フォーカス、`--ongoing-only` で active lineage のみ
- `traceary tail / list / search` に `--fields` (列順指定)、`--preset` (built-in: `failures` / `prompts-only` / `compact-summaries` + user-defined)、`--color=auto|always|never` highlight を追加 (`NO_COLOR` + terminal-injection 対策込み)
- Codex hooks が `UserPromptSubmit` をサポート、公式 Plugin Directory レイアウトに準拠
- `traceary doctor` に `<client>-host-capabilities` informational check を追加し、2026 Q2 時点の host 機能ギャップ (Claude `SubagentStop` / `PreCompact`、Codex memory feature flag、Gemini 0.38.x memory-manager preview) を surface
- MCP tool metadata / annotations 監査で Tool Search 時代に適合

### 変更
- Codex 標準化 (v0.7-1) により Codex 統合が `/plugins` フローと Plugin Directory 規約に一致
- Read コマンドのデフォルト出力は fields / preset / color サーフェスで駆動 (`--wide --utc` は v0.6.1 バイト一致を維持)
- durable-memory の list criteria が `Sources()` を SQLite 層まで push-down し、inbox の `--source` が pagination 正しく動作

### 修正
- `traceary memory import codex` が symlink した MEMORY.md を EvalSymlinks 解決前の Lstat で拒否 (memory root 内の他ファイルへのリダイレクトを防ぐ)
- Bridge import の dedupe 事前ロードで実 SQLite が要求する limit>=1 を遵守 (Codex memories import 側の latent bug も同時修正)
- `memory export --out` が既存の CLAUDE.md / AGENTS.md / GEMINI.md の手書きセクションを破壊しなくなり、管理マーカー部分のみを置換
- `tail --follow-session` が filter 前の unfiltered batch で cursor を前進させるため、非一致 session の trafic が scan 窓を塞がない
- `session tree --ongoing-only` が `status=stale` セッションを除外 (end event が無いことを理由に ongoing 扱いしていた問題)
- sanitizer / size-guard / parser の堅牢性を memories import / bridge import 経路でまとめて改善

### 対応 issue
- #551 v0.7-1 Codex plugin directory
- #575 v0.7-2 Codex UserPromptSubmit capture
- #576 v0.7-3 Codex memories import
- #552 v0.7-4 host matrix / doctor / smoke refresh
- #553 v0.7-5 tail / list / search の configurable columns
- #554 v0.7-6 saved view presets
- #555 v0.7-7 highlight / color with NO_COLOR support
- #556 v0.7-8 `tail --follow-session`
- #557 v0.7-9 durable-memory review inbox
- #560 v0.7-10 CLAUDE.md / AGENTS.md / GEMINI.md bridges
- #561 v0.7-11 subagent lineage in session tree
- #568 v0.7-12 durable-memory hygiene (redaction / expiry / duplicate)
- #569 v0.7-13 MCP tool metadata / annotations audit

## [v0.6.1] - 2026-04-15

`tail` / `list` / `search` / `timeline` のターミナル視認性を改善したリリースです。

### 追加
- `traceary timeline` の workspace ごとのサブロウ表示と、`compact_summary` → 最初の `prompt` → kind count フォールバック順で選ばれるアクティビティ要約、さらに JSON 出力の `workspace_breakdown` 配列
- `tail` / `list` / `search` / `timeline` のテキスト出力に `--utc` フラグを追加（デフォルトは現地時刻のまま）
- `presentation/cli/event_text_formatter.go` を共通ヘルパーとして新設し、compact / wide 行フォーマッタや timestamp / session / workspace の短縮ロジックを `tail`, `list`, `search` で共有
- `docs/cli/README.md` / `docs/cli/README.ja.md` に `traceary timeline` セクションを追加。トップレベル README には「直近の動きを確認する」セクションと tail / timeline の使用例を追加

### 変更
- `traceary tail`, `traceary list`, テキストモードの `traceary search` のデフォルト出力を、約 100 カラムに収まる 1 行コンパクト形式 (`HH:MM:SS  kind  sess=<先頭8文字>  ws=<basename>  message`、現地時刻) に変更。`--wide` で従来の 7 カラム tab 区切りを復元でき、`--wide --utc` を組み合わせると v0.6.1 以前の出力をバイト単位で再現
- `traceary timeline` のブロックヘッダに `total events:` ラベルを追加

### 修正
- `traceary timeline` が workspace 空の legacy 行を breakdown や JSON `workspaces` 配列に漏らさないように修正
- `traceary timeline` で whitespace-only な `compact_summary` / `prompt` body が同一ブロック内の後続有効サマリ候補を上書きしていた問題を修正

### 対象イシュー
- #538 compact tail output with local TZ
- #539 timeline activity summary with per-workspace breakdown
- #540 add README examples for tail and timeline workflows
- #541 compact list and search output with local TZ parity

## [v0.6.0] - 2026-04-14

アーキテクチャ整合性と runtime entrypoint の整理を中心に進めたリリースです。

### 追加
- 4 層境界、runtime entrypoint の原則、`scripts/` の役割を明文化した software architecture guide
- session boundary / command audit / prompt capture / compact-summary capture を Go 側で受ける `traceary hook ...` サブコマンド
- Codex 向けの user-facing entrypoint である `traceary integration codex install` / `uninstall`
- maintainer 向け Python helper を `go run ./cmd/repo-tooling ...` へ移すための移行ガイド

### 変更
- packaged hook の生成先を、embedded な runtime shell script ではなく Go runtime entrypoint へ切り替えた
- repository 全体の Optional API を規約どおり `Some` / `None` / `Value` ベースへ移行し、旧 API は互換 alias として残した
- 代表的なテスト群で inline assertion と `cmp.Diff` を優先する形へ寄せた

### 修正
- hook runtime state の後始末を best-effort のまま安全側に寄せ、duplicate end marker や wrapper 経由の parent state をより堅牢に処理するよう改善した
- managed hook の判定、merge、Codex plugin の install/uninstall が、custom wrapper path や unrelated custom hook を壊さないよう修正した

### 対象イシュー
- #459 Optional[T] API の規約差分整理
- #506 software architecture 原則と runtime boundary の文書化
- #507 hook runtime logic の Go サブコマンド化
- #508 embedded runtime shell asset からの packaged hooks 移行
- #509 user-facing / maintainer workflow に残る Python 依存の整理
- #522 Codex 向け Python install helper の Go entrypoint 化
- #523 maintainer 向け Python helper の Go repo tooling への移行
- #525 Optional[T] の convention API への移行
- #527 shared Go testing convention に沿ったテスト整理

## [v0.5.2] - 2026-04-14

documentation の正確性、導線、GoDoc の磨き込みをまとめたリリースです。

### 追加
- durable memory の考え方と運用フローをまとめた `docs/memory/` の英日ガイド

### 変更
- storage / interactive guide を更新し、実際に出荷している `tail` / `handoff` / durable memory の挙動と一致させた
- public docs と operator 向け CLI help の `workspace` 用語をそろえた
- README / docs index の導線を、3 層モデル、lifecycle、hook contract、durable memory を中心に再構成した
- release/support guide を、現在の maintainer review flow と release automation に合わせて更新した
- `go doc` から層境界と compatibility surface を追いやすいように package/interface comment を厚くした

### 対象イシュー
- #500 実装済み挙動に合わせた storage / interactive docs の更新
- #501 docs と CLI help に残る workspace 用語の整理
- #502 docs 導線の改善と durable memory concept guide の追加
- #503 現在の maintainer workflow に合わせた release/support docs の更新
- #504 architecture discovery のための GoDoc package/interface comments 強化

## [v0.5.1] - 2026-04-14

read-side の使い勝手改善と documentation follow-up のリリースです。

### 追加
- 新規イベントを追跡表示する `traceary tail` コマンド (NDJSON 出力対応)
- 各リリースタグの changelog カバレッジをリリース前に強制する CI ジョブ

### 変更
- README を 3 層 memory model と host capability マトリクスを軸に再構成
- v0.2.2 〜 v0.4.0 の changelog エントリを補完

### 修正
- `traceary tail` follow mode の各 poll window を SQLite 単一 read snapshot の中でスキャンするよう変更。並行書き込み下で OFFSET ページングがイベントを silently に取りこぼすデータ欠落バグを修正
- durable memory extraction が caller 指定 redaction パターンで正規化した後のキーで既存 fact を dedupe するようになり、extra redact pattern をローテーションしても重複候補が発生しなくなった
- artifact-ref extraction を拡張し、dotted path や末尾句読点をカバーしつつ slash-prose の誤検出を除外

### 対象イシュー
- #473 memory candidate 用の artifact-ref extraction 拡張
- #474 redaction パターン変更後の extracted memory dedupe
- #477 v0.2.2 〜 v0.4.0 の changelog 補完
- #478 memory model と host capability を軸にした README 再構成
- #481 Traceary event stream の live tailing
- #483 CI とリリース準備での changelog coverage 強制
- #489 並行書き込み時の tail ページング取りこぼし修正
- #490 redaction パターン正規化を検証する dedupe テスト強化
- #491 tail 境界 dedupe と From 包含契約の固定

## [v0.5.0] - 2026-04-13

Durable memory と context-aware workflow を導入するリリースです。

### 追加
- typed scope / evidence ref / artifact ref / lifecycle を備えた first-class durable memory ドメインモデル
- durable memory の SQLite 永続化と query サポート
- durable memory 用 CLI コマンド群（`remember` / `propose` / `accept` / `reject` / `supersede` / `expire` / `list` / `search` / `show` / `extract`）
- `traceary handoff` / `traceary session handoff` / `traceary compact-summary` で共有する `ContextUsecase` ベースの structured handoff/context assembly
- MCP durable memory tools と memory-aware context retrieval
- session summary / compact summary / note・review・prompt signal からの candidate extraction

### 変更
- protected `main` 上の Homebrew maintenance PR 経路で、release workflow が GitHub App token を使うよう変更
- integration manifest と package metadata を `0.5.0` 系列へ更新

### 含まれるイシュー
- #457 Homebrew release PR を GitHub App token 経由に変更
- #453 audit log / working memory / durable memory の 3 層モデル定義
- #460 Memory aggregate と typed memory value object の導入
- #461 durable memory の SQLite 永続化と query サポート
- #462 manual memory lifecycle usecase と CLI コマンドの追加
- #463 ContextUsecase 導入と handoff/context 意味論の統一
- #464 MCP durable memory tools と memory-aware retrieval の追加
- #465 session / compact summary からの memory candidate extraction

## [v0.4.0] - 2026-04-12

timeline / prompt capture とアーキテクチャ堅牢化のリリースです。

### 追加
- `compact_summary` / `prompt` signal 向け EventKind 拡張と、`traceary log` / MCP `add_log` の `--kind` 対応
- `PostCompact` と `UserPromptSubmit` の generated hook、persisted compact-summary / prompt event の記録
- workspace 単位の活動観測に使える `traceary timeline` と timeline block query
- 拡張された hook surface に対応する lifecycle / privacy ドキュメント

### 修正
- session boundary 永続化、duplicate session 処理、compact/prompt 時の agent 解決をより防御的に改善
- hook の install/read path で危険な symlink traversal を拒否
- `--db-path` / `TRACEARY_DB_PATH` を全 subcommand で一貫して尊重
- interface 統合時に落ちていた query / input validation を復元

### 変更
- presentation / usecase / queryservice / sqlite wiring を multi-method interface と aggregate ごとの datasource 構成へ統合
- repository / type の責務を `domain/` / `application/types` 側へ寄せ、`domain/port` を削除
- CLI / MCP の JSON/output struct、DTO、Optional 伝搬を整理

## [v0.3.0] - 2026-04-11

workspace rename と consolidated-usecase アーキテクチャのリリースです。

### 追加
- 新しい application-layer query surface を支える `Client` / `Workspace` value object と filter criteria DTO
- Event / Session / Store の consolidated usecase interface と service-factory ベースの composition path
- 次のアーキテクチャ段階に必要な repository interface と session label サポート

### 変更
- hooks / CLI / docs / storage-facing API 全体で repo / work-context の概念を `workspace` へ改名
- datasource 構築時の DB path 注入へ移行し、presentation / MCP wiring を consolidated usecase ベースへ移行
- release checklist / dependabot / pinned actions / release-drafter split など、リリース運用まわりも更新

### 修正
- MCP session handoff が `session_id` を正しく引き継ぐよう修正
- workspace rename 後に残っていた `repo` 参照と generated plugin hook drift を除去
- release automation と review follow-up を tag 前に反映

## [v0.2.5] - 2026-04-11

session lifecycle と queryservice cleanup のリリースです。

### 追加
- `traceary session tree --json`
- session list 日付 filter 用の `--since` / `--until` alias
- `TRACEARY_PARENT_SESSION_ID` 経由の parent-session 伝搬

### 修正
- handoff / compact-summary が要求された session filter を session lookup に正しく渡すよう修正
- session end、duplicate session start、stale-session GC、invalid parent-session 入力時の扱いをより厳密に改善
- doctor のバージョン比較で build metadata を除去してから判定

### 変更
- stale session close を専用 usecase に抽出
- queryservice consumer interface を `domain/port` へ移動
- 残っていた inline SQL を embedded `.sql` に抽出

## [v0.2.4] - 2026-04-11

MCP audit enrich の patch リリースです。

### 追加
- `tool_input` が空のとき、MCP audit payload が `tool_name` に fallback

### 修正
- `traceary doctor` が Claude plugin install を正しい hook source として認識
- すでに終了済み session を終わらせたとき、黙殺ではなく warning を出すよう改善

### 変更
- 新しい patch release 運用に合わせて release/version-bump automation を更新

## [v0.2.3] - 2026-04-11

`v0.2.2` 向け review-fix patch リリースです。

### 修正
- `v0.2.2` 系列に対する review follow-up を反映

### 変更
- Homebrew formula metadata を `v0.2.2` リリース状態へ更新

## [v0.2.2] - 2026-04-11

query surface の使い勝手を高める patch リリースです。

### 追加
- `traceary list --from/--to`
- `traceary session list --client`
- client / agent / workspace / session / kind に対応した `list_events` MCP filter
- `traceary backup create` の positional argument 対応

### 修正
- `traceary show` が command-audit event の `exit_code` を表示
- `traceary search --failures` を有効な structured search constraint として扱うよう修正
- `traceary list --kind audit` が command-audit event を正しく解決
- CLI date parsing と session-list date-range validation を一貫化し、逆転範囲も拒否

## [v0.2.1] - 2026-04-11

v0.2.0 で残したスコープの補完リリースです。

### 追加
- `traceary session gc` コマンド（stale セッションの自動終了、`--dry-run` 対応）
- `session_handoff` MCP ツール
- `traceary search --failures` フラグ
- compact-summary テスト、golden file テスト
- hook 共通関数の集約

### 変更
- search_events SQL の go:embed 外出し
- goreleaser の formula 生成修正（`skip_upload: true`）
- release workflow に Homebrew PR の auto-merge 追加

## [v0.2.0] - 2026-04-11

コンテキスト保全と本番運用対応のリリースです。

### 追加
- compact/clear 後の自動コンテキスト保全（PostCompact + SessionStart hooks）
- `traceary compact-summary` コマンド（LLM不要のコンテキストポインタ生成）
- `traceary session handoff` コマンド（簡潔なセッション状態サマリー）
- `traceary session tree` コマンド（親子セッション階層の可視化）
- `traceary list --failures` フラグ（失敗コマンドのフィルタ表示）
- `exit_code` カラム追加（マイグレーション 000005）
- `traceary doctor` のバージョンチェック
- Hook contract ドキュメント（Claude/Codex/Gemini の能力ティア定義）
- 統合 contract テスト、マイグレーション回帰テスト

### 変更
- Gemini CLI の AfterTool を全ツール対応に拡張
- 24h 超の active セッションを stale として表示
- README を CLI ファーストのインストールフローに再構成
- Makefile を英語化 + ターゲット追加
- テスト名を全て日本語から英語に移行
- リポジトリインターフェースを `domain/port` に移動
- `list_sessions` の SQL を go:embed ファイルに外出し

### 含まれるイシュー
#236-#254（20件、#255 は合理的にクローズ）

## [v0.1.19] - 2026-04-10

この release では、CLI の可視性を高め、redaction を黙って弱める config 異常を見えるようにし、hook asset の重複管理を解消しました。

### 追加
- `traceary doctor` が `config.json` の状態を missing / loaded / unreadable / invalid で判別して表示
- 壊れた config により追加 redaction パターンが無効化されるとき、CLI / MCP の config 読み込みで警告を表示
- hook script の改行正規化と required flag セットアップ挙動の回帰テスト

### 修正
- `traceary session list` の text / JSON 出力で `label` / `summary` / `parent_session_id` を一貫して表示
- `traceary session label` と `session list` の拡張メタデータを CLI docs / top-level docs に反映
- セッションメタデータの表形式出力で tab / newline を正規化し、ターミナル表示崩れを防止
- package 化された hook script の改行を install 前に LF へ正規化し、Windows checkout 時の `/bin/bash\r` shebang 退行を防止

### 変更
- packaged hook asset を重複した文字列リテラルではなく、canonical な `scripts/hooks/*.sh` から生成するよう変更
- 残っていた Cobra required flag セットアップ時の panic を、required flag の挙動を維持したまま graceful error に置換
- 統合マニフェストのバージョンを `0.1.19` に更新

### 含まれるイシュー
- #219 CLI 出力と docs でセッションメタデータを一貫表示
- #220 config 読み込み失敗をオペレーターに可視化
- #221 hook script を packaging / test 共通の単一ソースに統一
- #222 残る CLI setup panic を graceful error に置換

## [v0.1.18] - 2026-04-10

セッションメタデータの充実とデータ品質の向上を行いました。

### 追加
- `sessions` テーブル新設 + 既存 events からのバックフィルマイグレーション
- `traceary session label <text> --session-id <id>` コマンド — セッションにラベルを付与
- `session list --label` フィルタ
- `traceary session end --summary` フラグ — セッション終了時にサマリーを記録
- `traceary session start --parent-session-id` フラグ — サブエージェントの親子関係を記録
- Claude Code hooks で MCP ツール呼び出し（`mcp__.*`）を記録
- Gemini CLI ワンコマンドインストールスクリプト

### 修正
- audit hooks でセッション開始時の repo を永続化し、CWD ベースの repo drift を防止
- MCP ツール呼び出し時に `tool_name` へ fallback するよう audit スクリプトを改善
- 日付バリデーションを queryservice 層に集約（infra 層の冗長なバリデーションを除去）
- doctor 設定チェックの `localizef` 引数過剰を修正

### 変更
- `session list` クエリを sessions テーブル主体に書き換え（events JOIN で集計）
- `SessionSummary` DTO に `label`/`summary`/`parent_session_id` を追加
- 統合マニフェストのバージョンを `0.1.18` に更新

### 含まれるイシュー
- #196 日付バリデーションの queryservice 層集約
- #200 hooks による MCP ツール呼び出し記録
- #201 セッションにラベル/タスク名を付与
- #202 セッション間の親子関係を記録
- #203 repo フィールドの正規化
- #204 セッション終了時にサマリーを記録
- #206 セッションメタデータモデルの導入
- #207 Gemini CLI インストール体験の改善

## [v0.1.17] - 2026-04-09

マルチエージェントワークフローの改善と CLI の使い勝手向上を行いました。

### 追加
- `traceary session list` コマンド — セッションごとの集約情報（ステータス、所要時間、イベント/コマンド数、エージェント内訳）を表示
- Claude Code のサブエージェント識別 — hooks が payload の `agent_type` を読み取り、サブエージェント内実行時に階層的なエージェント名（例: `claude/Explore`）を記録
- `session list` の `--from`/`--to` 日付フィルタ（YYYY-MM-DD バリデーション付き）

### 修正
- `list`/`search` のテーブル出力で MESSAGE カラムを 80 文字に切り詰め、改行を正規化 — 長いコマンドボディによるターミナル表示崩れを防止
- DB 初期化時の `chmod(0600)` エラーを best-effort に変更 — read-only ファイルシステムや他ユーザー所有の DB でも読み取り系コマンドが動作

### 変更
- CLAUDE.md / AGENTS.md / GEMINI.md に「1 issue = 1 branch = 1 PR（例外なし）」ルールを追加
- 統合マニフェストのバージョンを `0.1.17` に更新

### 含まれるイシュー
- #185 list/search 出力の MESSAGE カラム切り詰め
- #186 セッションサマリーコマンド (`session list`) の追加
- #187 Claude Code のサブエージェント識別対応
- #188 read-only ファイルシステムでの読み取りコマンド安全化
- #194 1-issue-1-PR ルールのガードレール追加

## [v0.1.16] - 2026-04-09

この release では、コード品質の改善、ユーザー設定可能な監査リダクションパターンの追加、CLI / MCP サーバー全体での debug レベル診断の充実を行いました。

### Added
- `~/.config/traceary/config.json` による監査リダクションパターンのユーザー設定 — 追加の正規表現パターンが CLI（`traceary audit`）と MCP サーバー（`add_audit`）の両方で組み込みルールの後に適用される
- `infrastructure/sqlite/` 全体で、抑制されていたクリーンアップエラーの debug レベルログ出力
- セッションおよびリポジトリコンテキスト解決の各フォールバック段階での debug レベルログ出力
- AI エージェントの動作統一のための `CLAUDE.md`、`AGENTS.md`、`GEMINI.md` プロジェクト規約ファイル
- `LoadConfig`、`compileExtraRedactPatterns`、`setupLogger` のテスト追加

### Fixed
- `init()` 内の `log.Fatalf` を `run()` からの graceful error return に置換 — 不正な `LOG_LEVEL` でスタックトレースではなくクリーンなエラーメッセージを出力
- MCP サーバーがリクエストごとではなく起動時に一度だけ config を読み込むよう修正

### Changed
- エージェント設定ファイルにイシュークローズポリシーを追加（実装 PR はサブイシューのみクローズ、親イシューはリリース PR でクローズ）
- AI エージェント設定ファイルを docs i18n チェックから除外
- 環境リファレンスドキュメントに config.json と追加リダクションパターンの説明を追記
- release 向け integration manifest version を `0.1.16` に更新

### Included issues
- #170 Replace panic calls in CLI initialization with graceful errors
- #171 Make audit redaction patterns user-configurable
- #172 Add debug logging for suppressed cleanup errors
- #173 Clarify error propagation in session resolution logic

## [v0.1.15] - 2026-04-09

この release では、`v0.1.14` の dogfood で残っていた follow-up を閉じ、ローカル専用 Git repository の扱いと `traceary doctor` の first-run 表示を実用的に整えました。

### Fixed
- `remote.origin.url` が無いローカル専用 Git repository でも、Git worktree ルートへ fallback するようにし、`traceary log` / `traceary audit` が active session を再利用できるようにした
- packaged hook script でも同じ fallback を使うようにし、Claude / Codex / Gemini integration と CLI の挙動をそろえた
- `traceary doctor` が host config 未作成などの first-run 状態を generic failure ではなく `warn` として返すようにした
- setup ガイダンスを妨げる hook script materialize 問題は、必ずしも壊れた install を意味しないため、より明確な `warn` メッセージで出すようにした

### Changed
- root README と CLI / hooks / environment docs に、local-only Git worktree fallback と `traceary doctor` の `warn` / `fail` の意味を追記した
- release 向け integration manifest version を `0.1.15` に更新した
- release guide の固定 tag 例を `v0.1.15` に更新した

### Included issues
- #165 Make doctor clearer on first-run integration states
- #166 Improve work-context detection for local-only git repositories

## [v0.1.14] - 2026-04-09

この release では、`v0.1.13` 以降に main へ入っていた integration / runtime 修正と、それを公開するための release metadata 整備をまとめて収録しています。

### Fixed
- 共通 `SessionEnd` hook を冪等化し、Gemini が session-end hook を重複で呼んでも `session_ended` を 1 回だけ記録するようにした
- Codex の local install helper が active plugin cache の配置、`codex_hooks` の有効化、`~/.codex/hooks.json` への Traceary hook マージまで行うよう修正した
- Codex の uninstall helper が `config.toml` 内の nested plugin subtable までまとめて消すよう修正した
- GoReleaser が生成する Homebrew formula の test を `traceary --version` に修正した

### Changed
- root README と host integration docs を、手動 CLI より plugin / extension 導入を先に案内する構成へ整理した
- release 向け integration manifest version を `0.1.14` に更新した
- 抜けていた `v0.1.12` / `v0.1.13` の changelog を補完し、release guide の古い固定 tag 例を解消した

### Included issues
- #159 Codex local install does not activate the Traceary plugin runtime
- #160 Gemini extension records duplicate `session_ended` events
- #161 Root README should prioritize plugin and extension install flows
- #163 Align release metadata with the current release line
- #164 Use --version in the generated Homebrew formula test

## [v0.1.13] - 2026-04-09

### Added
- `traceary log`, `traceary audit`, `traceary session {start,end,latest,active}` に `--json` を追加
- `traceary list` に `--kind`, `--client`, `--agent`, `--session-id`, `--repo` の構造化 filter を追加

### Changed
- `traceary session latest` を、同一 session context 内で最新の lifecycle boundary を優先する意味に再定義した
- manual command の default / JSON 出力 / hooks guidance を CLI help と docs で分かりやすく整理した

### Fixed
- 同じ session が複数回 start された場合でも、最新の `session_started` を優先するよう修正
- 同じ `session_id` を別 repo / agent が再利用している場合、他 context の lifecycle boundary を拾わないよう修正
- cross-context の latest-session / active-session lookup に対する regression coverage を追加

### Included issues
- #146 dogfood ergonomics follow-up
- #147 fix session latest semantics for ended sessions
- #148 align machine-readable output for mutating and session helper commands
- #149 improve `traceary audit` ergonomics
- #150 add structured filters to `traceary list`
- #151 surface environment variables and defaults in CLI help
- #152 improve `hooks print` discoverability
- #153 clarify and standardize manual CLI defaults

## [v0.1.12] - 2026-04-09

### Added
- Claude Code / Codex / Gemini CLI 向けの共通 native integration contract
- Traceary の MCP / hooks / commands / skill surface を含む Claude Code plugin package
- Traceary の MCP / hooks / commands / skill surface を含む Codex plugin package
- Traceary の MCP / hooks / commands / skill surface を含む Gemini CLI extension package
- integration validation / packaging coverage と install / update / uninstall / smoke-test guidance

### Included issues
- #140 native agent integrations
- #141 define the shared integration contract
- #142 publish a Claude Code plugin
- #143 publish a Codex plugin
- #144 publish a Gemini CLI extension
- #145 add install/update/uninstall/doctor guidance and smoke tests

## [v0.1.11] - 2026-04-09

### Added
- README やリリース画面で使える最小限の Traceary マーク

### Changed
- トップレベル README を短い導入ページに整理し、詳細な導線を docs 索引へ集約
- `docs/README.md` / `docs/README.ja.md` を詳細ドキュメントの中心として再構成
- README / CONTRIBUTING / 主要ガイドの日本語表現を見直し、より自然な文体に調整
- 脆弱性の連絡先を `CONTRIBUTING.md` / `CONTRIBUTING.ja.md` に集約し、独立した `SECURITY.md` / `SECURITY.ja.md` を廃止

### Included issues
- #133 public surface polish
- #134 rewrite Japanese docs into natural Japanese
- #135 simplify README and reduce link sprawl
- #137 reorganize docs landing pages and cross-links
- #138 reassess and minimize the security-policy footprint
- #139 add a minimal visual identity

## [v0.1.10] - 2026-04-09

### Fixed
- GoReleaser の Homebrew 設定が生成された archive ID を参照するよう修正し、tagged release で archive と tap formula を再び正常に公開できるようにした

## [v0.1.9] - 2026-04-09

### Added
- `--yes` 付きの安全な backup restore フロー
- mutating command 向けの script-friendly な `--id-only`
- `traceary audit` の named flags (`--command`, `--input`, `--output`)
- CLI / environment / storage / operations / interactive の専用 docs
- GoReleaser formula automation による Homebrew 配布導線
- recent events parity のための read-only MCP tool `list_events`
- Bash / Zsh / Fish / PowerShell 向け `traceary completion`

### Changed
- onboarding と hooks docs は guided setup と failure-mode check をより早く辿れるよう改善
- `traceary log` / `traceary audit` は、まず解決済み repo / work context に対する最新 non-stale active session を再利用し、見つからない場合だけ `default` に fallback するよう変更
- 公開向け README に CI / release badge と、privacy / no-telemetry / support posture を追加
- hooks / storage / operations docs で runtime assumption をより明示的に文書化

### Included issues
- #106 onboarding and daily-use ergonomics
- #107 safer backup restore flow
- #108 script-friendly mutating command output
- #109 named audit flags
- #110 CLI / env reference docs
- #111 onboarding / first-run docs
- #112 Homebrew distribution flow
- #113 guided setup for supported clients
- #114 storage model / schema / gc docs
- #115 active session defaults for manual log / audit
- #116 hook edge cases and failure-mode docs
- #117 MCP read workflow parity
- #118 public OSS trust and polish
- #119 concurrency / hook-state assumptions
- #120 interactive inspection ergonomics

## [v0.1.8] - 2026-04-08

### Added
- DB と hooks 設定を診断する `traceary doctor` / `traceary status`
- 公開向け `SECURITY.md` / `SECURITY.ja.md`
- `traceary backup create` / `traceary backup restore`
- `docs/backup/` 配下のバックアップ / マシン移行ガイド
- MCP の session lifecycle tools: `start_session`, `end_session`, `latest_session`, `active_session`

### Changed
- `hooks install` は既存の対応 client config に対して、既定で Traceary 管理下 hook を merge するよう改善
- portable hook scripts は runtime で `python3` を不要化
- `traceary audit` は common な secret っぽい値を保存前に伏せ字化し、CLI / MCP 出力で redaction を通知するよう変更
- 公開向け README / hooks / MCP 文書で command surface と platform support の説明を整合
- `traceary list` / `traceary search` は `--offset` による安定した pagination をサポート

### Included issues
- #88 operational safety and public usability
- #89 safe hooks config merge
- #90 doctor / status diagnostics
- #91 audit secret persistence hardening
- #92 public security policy
- #93 README / platform support alignment
- #94 list / search pagination
- #95 MCP session ergonomics
- #96 backup / export / import story
- #97 reduce hook runtime dependency friction

## [v0.1.7] - 2026-04-08

### Added
- MIT の `LICENSE` を追加
- 公開向け `CONTRIBUTING.md` / `CONTRIBUTING.ja.md` を追加
- `docs/mcp/` に公開向け MCP integration ガイドを追加

### Changed
- `traceary session end` で flag を省略した場合、対応する `session_started` から `client` / `agent` / `repo` を継承するように変更
- 公開向け install / release 導線と GitHub Actions の release 自動化を追加
- CLI の operator-facing message は既定で英語とし、日本語は `TRACEARY_LANG=ja` で opt-in に変更
- hooks install は既定で source checkout 外の portable script を materialize するように変更

### Included issues
- #72 public release readiness
- #73 project license の追加
- #74 session end の attribution 継承
- #75 公開向け CLI 英語化
- #76 公開 install / release distribution flow
- #77 source checkout 非依存の hooks install
- #78 CONTRIBUTING guide の追加
- #79 公開向け MCP server integration 文書

## [v0.1.6] - 2026-04-08

### Changed
- `traceary init` が「任意の明示 bootstrap」であることを help / docs で明確化
- `traceary session end` が session ID ではなく記録した event ID を返すように変更
- `hooks --client` が `claude-code`, `codex-cli`, `gemini-cli` を alias として受け付けるように改善
- Cobra 由来の positional-argument エラーを日本語化

### Included issues
- #60 `traceary init` の役割と暗黙 DB 作成の整理
- #61 `traceary session end` の出力契約整理
- #62 `hooks print --client` の discoverability 改善
- #63 CLI 引数エラーの日本語化

## [v0.1.5] - 2026-04-08

### Changed
- `search --kind` の discoverability を改善
- すべての CLI コマンドで `TRACEARY_DB_PATH` をサポート
- CLI 失敗時の stderr を plain `Error: ...` に統一

### Included issues
- #53 `search --kind` の discoverability 改善
- #54 `TRACEARY_DB_PATH` サポート
- #55 plain CLI error output

## [v0.1.4] - 2026-04-08

### Added
- README / README.ja.md に Quick Start
- `traceary hooks install`
- `traceary context` / `traceary handoff`

### Changed
- `search` に構造化フィルタを追加
- active session の stale 判定を追加
- audit truncation を設定可能に改善

### Included issues
- #40 Quick Start
- #41 hooks install
- #42 structured search filters
- #43 context handoff
- #44 stale session handling
- #45 audit truncation configuration

## [v0.1.3] - 2026-04-08

### Fixed
- `session latest` / `session active` の no rows エラー二重化を解消
- session lookup の not-found 判定を sentinel error ベースに整理

### Included issues
- #37 `session latest/active` の no rows エラー修正

## [v0.1.2] - 2026-04-08

### Added
- `traceary list`, `traceary search`, `traceary show` に `--json`
- `traceary session active`

### Changed
- `session latest` の no-rows 判定を修正
- `RootCLI` の依存注入を `RootCLIOptions` に整理

### Included issues
- #28 `session latest` の no rows 判定修正
- #29 主要読み取りコマンドの JSON 出力
- #30 active session 取得導線
- #31 command audit output 検索の再確認
- #32 `RootCLIOptions` による依存注入整理

## [v0.1.1] - 2026-04-08

### Added
- `traceary show <event-id>`
- `traceary session latest`
- `traceary hooks print --client <...>`

### Changed
- hooks 設定例の出力が CLI から直接行えるようになり、dogfood の手順を短縮
- `hooks print` の既定 binary 解決を安定した `traceary` コマンド名に修正

### Included issues
- #19 dogfood usability improvements
- #20 `traceary show <event-id>`
- #21 `traceary session latest`
- #22 `traceary hooks print --client`
- #26 `hooks print` の follow-up 修正

## [v0.1] - 2026-04-07

### Added
- SQLite ベースの local store
- `traceary init`, `log`, `audit`, `list`, `search`, `session start/end`, `gc`
- MCP server (`add_log`, `add_audit`, `search`, `get_context`)
- Claude Code / Codex CLI / Gemini CLI 向け hooks integration

### Included issues
- #11 bootstrap CLI and SQLite store
- #12 log / list
- #13 session start / end
- #14 audit log
- #15 gc / retention
- #16 search / work context
- #17 MCP server
- #18 hooks integration

[v0.1]: https://github.com/duck8823/traceary/releases/tag/v0.1
[v0.1.1]: https://github.com/duck8823/traceary/releases/tag/v0.1.1
[v0.1.2]: https://github.com/duck8823/traceary/releases/tag/v0.1.2
[v0.1.3]: https://github.com/duck8823/traceary/releases/tag/v0.1.3
[v0.1.4]: https://github.com/duck8823/traceary/releases/tag/v0.1.4
[v0.1.5]: https://github.com/duck8823/traceary/releases/tag/v0.1.5
[v0.1.6]: https://github.com/duck8823/traceary/releases/tag/v0.1.6
[v0.1.7]: https://github.com/duck8823/traceary/releases/tag/v0.1.7
[v0.1.8]: https://github.com/duck8823/traceary/releases/tag/v0.1.8
[v0.1.9]: https://github.com/duck8823/traceary/releases/tag/v0.1.9

[v0.1.10]: https://github.com/duck8823/traceary/releases/tag/v0.1.10
[v0.1.11]: https://github.com/duck8823/traceary/releases/tag/v0.1.11
[v0.1.12]: https://github.com/duck8823/traceary/releases/tag/v0.1.12
[v0.1.13]: https://github.com/duck8823/traceary/releases/tag/v0.1.13
[v0.1.14]: https://github.com/duck8823/traceary/releases/tag/v0.1.14
