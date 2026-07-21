# ワークスペース識別契約

[English](./workspace-identity-contract.md)

状態：v0.30.0 の実装 Issue #1435 と #1429 に適用する設計契約。

## 要求

Traceary は session と event の両方に `workspace` を保存していますが、二つの値の意味を明確に分けていません。
hook state は session 開始時に選んだ workspace を後続 event へコピーする傾向があります。
この処理は session 内の値を揃えますが、ある repository で開始した agent が別の repository で command を実行した事実を表せません。

この契約では、次の二つを別の事実として定義します。

- session は変更しない一つの **canonical workspace（基準ワークスペース）**を持つ。
- event は event が発生した場所を示す **effective workspace（実行時ワークスペース）**を持つ。

既存の session ID、event ID、外部キー、`workspace` column、既定 filter は維持します。
重複 row の削除、本文の一致に基づく alias 推定、one-shot terminal lifecycle の変更は対象外です。

## 現在の schema と用語

現在の物理 schema は aggregate ごとに workspace column を一つ持っています。

| 既存 column | v0.30 の意味 | 定義 |
|---|---|---|
| `sessions.workspace` | canonical workspace | session row を最初に作成したときに決まり、その後は変わらない session attribution |
| `events.workspace` | effective workspace | event を記録するときに event ごとに決まる attribution |

migration ではこれらの column を rename しません。
rename は情報を増やさず、古い binary と script を壊すためです。
application と presentation は明示的な `CanonicalWorkspace` と `EffectiveWorkspace` を追加できますが、v0.x の互換期間では `Workspace` accessor を維持します。

空文字列は recorder が workspace を特定できなかったことを示します。
filesystem root、home directory、global scope の意味には使いません。

## 概念モデル

| 概念 | 状態 | 振る舞い | 不変条件 |
|---|---|---|---|
| session canonical workspace | 不明を許す一つの workspace | 最初の session start で選ぶ | 同じ session ID では変更しない |
| event effective workspace | 不明を許す一つの workspace | event delivery ごとに解決する | session の canonical attribution を書き換えない |
| workspace observation | 安定した observation ID、host の生値、正規化した workspace、host と source の情報 | attribution の決定根拠を記録する | append-only の provenance であり、event 本文を持たない |
| workspace relationship | exact、descendant、ancestor、explicit alias、conflict、unknown | canonical と observation の関係を分類する | 分類は二つの workspace 値を変更しない |
| 互換 workspace filter | surface ごとの既存 selector | v0.29 の `--workspace` または `workspace` の意味を適用する | 既定の結果集合を広げない |

canonical workspace は「session がどこで始まり、どこを基準にするか」に答えます。
effective workspace は「その event がどこで発生したか」に答えます。
複数 repository にまたがる session は、一つの canonical 値と、同じ session ID に属する複数の event-local effective 値で表現します。

### Workspace 値の規則

Workspace 文字列は trim 後も opaque な domain value として扱います。
`github.com/org/repo` のような Git remote identity と、正規化した絶対 path の両方を許可します。
複数の worktree、fork、remote があり得るため、remote identity と local checkout path を自動的に同じ workspace だとは判定しません。

path の正規化では host の path 規則に従い、重複 separator と `.` segment を除去できます。
symlink の解決、network access、read 時に repository が実在することは要求しません。
正規化によって値が変わる場合は、正規化前の値を observation provenance に残します。

## Attribution の決定

### Session canonical workspace

最初に session row を作成した時点で canonical workspace を固定します。
後続の retry、resume hook、event delivery、end hook は変更できません。

session start は、次の順に最初の利用可能な値を選びます。

1. operator が明示した `TRACEARY_WORKSPACE`。
2. host-native な session start workspace または workspace path。
3. session start 時の current working directory から検出した repository identity。
4. session start 時の current working directory を正規化した絶対 path。
5. unknown（`""`）。

冪等な start retry が異なる workspace を送る場合があります。
retry は保存済み canonical workspace を維持し、新しい値を canonical 値との関係が分類された observation として記録します。
#1435 の cross-host delivery identity によって retry だと確定した場合は、新しい論理 `session_started` event を追加しません。

child session は、child start の明示的な証拠から自身の canonical workspace を選びます。
child start に workspace の証拠がない場合だけ、parent canonical workspace を継承します。
継承は fallback であり、parent と child の event が同じ repository で発生することを要求しません。

### Event effective workspace

event ごとに session canonical workspace とは別に effective workspace を解決します。
次の順に最初の event-local 値を選びます。

1. operator が明示した event override。
2. tool 固有の working directory または host の event workspace field。
3. hook payload の current working directory。
4. event-local directory から検出した repository identity。
5. 保存済み session canonical workspace。
6. unknown（`""`）。

host adapter は payload をこの決定入力へ正規化します。
application rule は host JSON の field 名を知りません。
選んだ effective workspace は event に保存し、session row へ書き戻しません。

boundary event は、host が明確な boundary-local workspace を送らない限り session canonical workspace を使います。
どちらの場合も session row は変更しません。

### Relationship の分類

relationship の分類は診断情報であり、event の保存を拒否する条件にはしません。
次の順に一つの分類を決めます。

1. `unknown`：どちらかの値が空。
2. `exact`：正規化した値が一致。
3. `explicit_alias`：operator が review した alias が二つの値を関連付けている。
4. `descendant`：effective local path が canonical local path の配下。
5. `ancestor`：effective local path が canonical local path を内包。
6. `conflict`：両方の値が既知で、以上の関係に該当しない。

remote identity は、review 済みの明示的な record がある場合だけ alias になります。
本文の一致と時刻の近さは workspace alias の証拠にしません。

## 責務と境界

| 責務 | 所有者 | 所有しない層 |
|---|---|---|
| canonical と effective の value object、relationship rule | domain/application types | hook handler、SQLite scan |
| 正規化済み証拠から値を選ぶ orchestration | session/event usecase | host 固有 JSON adapter |
| host payload field の抽出 | `presentation/cli` の host adapter | domain model |
| immutable persistence と observation transaction | repository boundary | CLI/MCP serializer |
| 互換 filter の mapping | query criteria と query service | 個別の Cobra handler |
| 診断表示 | doctor/report presentation | persistence layer |

application boundary は boolean flag の集合ではなく、consumer-oriented な attribution input を受け取ります。

```text
SessionWorkspaceAttribution {
    explicit_workspace?
    host_workspace?
    event_local_directory?
    session_canonical_fallback?
    raw_observation?
    source_client
    source_hook
}
```

これは概念上の interface であり、Go DTO 名を固定する指定ではありません。
host adapter が session start と event recording 用のより小さい command を作る場合も、選択順と unknown 値の契約は観測可能にします。

observation がある場合、persistence は event と workspace observation を同じ transaction で commit します。
診断情報の write に失敗した場合は、完全な provenance があるように見える event を残さず、transaction を失敗させます。
任意の証拠が malformed で分類できない場合は `unknown` へ fallback できますが、生の observation と診断理由を維持します。

## Additive migration と backfill

実装 migration は additive とし、既存の二つの `workspace` column を維持します。
canonical または effective 値を新しい column へ複製せず、append-only provenance table と review 済み alias table を追加します。

```sql
CREATE TABLE session_workspace_aliases (
    session_id TEXT NOT NULL REFERENCES sessions(session_id) ON DELETE CASCADE,
    alias_workspace TEXT NOT NULL,
    reviewed_at TEXT NOT NULL,
    reviewed_by TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (session_id, alias_workspace)
);

CREATE TABLE session_workspace_observations (
    observation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    raw_workspace TEXT,
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    delivery_id TEXT,
    observed_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, observed_at DESC, session_id);

CREATE UNIQUE INDEX idx_session_workspace_observations_delivery
    ON session_workspace_observations(session_id, delivery_id)
    WHERE delivery_id IS NOT NULL AND delivery_id <> '';

CREATE UNIQUE INDEX idx_session_workspace_observations_event
    ON session_workspace_observations(event_id)
    WHERE event_id IS NOT NULL AND event_id <> '';
```

これらの table は attribution metadata だけを保存します。
command input、output、prompt text、transcript text、その他の event 本文を複製しません。
report は observation row から件数を計算しますが、件数は deduplication の判定ではありません。

alias は一つの session に限定し、reviewer と timestamp を持つ review 操作でだけ追加します。
alias を追加しても過去の `observed_relationship` は書き換えません。
report は ingest 時に観測した relationship と、review 済み alias を join して求めた現在の relationship を両方表示できます。

runtime の observation ID は、利用できる場合に #1435 が定義する安定した delivery identity から作り、それ以外は domain が生成します。
partial unique index は同じ delivery ID の exact redelivery を冪等にし、保存済み event ごとの attribution observation を一つに制限します。
同じ delivery ID を持たない正当な delivery は統合しません。
observation の `session_id` は意図的に外部キーにしません。
historical event と direct event write は materialize された `sessions` row がない session ID を持つ可能性があり、migration はその row を維持する必要があるためです。
該当する observation は session row が作られるまで `unknown` とし、review 済み alias の追加には外部キーによって実在する session を要求します。

backfill は schema transaction の中で次の処理を行います。

1. すべての `sessions` row と `events` row を変更しない。
2. 既存 event ごとに一つの observation を追加し、`observation_id` には `backfill:<event_id>` を使う。
3. event timestamp と event ID を `observed_at` と `event_id` に設定する。
4. 過去 row は値を保持していないため、`delivery_id` と `raw_workspace` は unknown のままにする。
5. `sessions.workspace` と比較し、exact と local path の ancestor/descendant rule だけで分類する。
6. session row またはいずれかの workspace が不明な場合は `unknown` にする。
7. alias row を自動作成せず、本文の一致を alias または delivery の証拠にしない。

backfill の source client と source hook は、利用できる場合に各 event から取得します。

実装では、空 workspace、materialize された session row がない event、複数 effective workspace にまたがる session、Windows path、exact redelivery の uniqueness、review 済み alias、event 削除後の `SET NULL`、migration 後の冪等な再 open をテストします。

### Rollback

rollback では backup を取得し、以前の binary を deploy して additive observation table と alias table を無視するか削除します。
以前の binary は変更されていない `sessions.workspace` と `events.workspace` を読み書きできます。
down migration は workspace column を書き換えず、event を削除しません。

migration が既存 row 数または ID を変えた場合、通常の event ingest を妨げた場合、既定 workspace filter の結果を広げた場合、dogfooding で既知 pair の 5% を超える conflict が見つかり host fixture で説明できない場合は rollback します。

## Query と出力の互換性

v0.30 は surface ごとに現在の selector の意味を維持します。

| Surface | 既存 `workspace` selector の対象 | 互換規則 |
|---|---|---|
| `list`、`search`、`tail`、MCP event read | event effective workspace | exact filter を維持する |
| `sessions`、session tree | session canonical workspace | exact filter を維持する |
| `session handoff`、memory/context pack | canonical session を優先し、既存の local descendant evidence fallback を使う | remote alias を推定しない |
| event JSON の `workspace` | effective workspace | field 名を維持し、意味を文書化する |
| session JSON の `workspace` | canonical workspace | field 名を維持し、意味を文書化する |

将来の明示 selector は `workspace_scope=effective`、`canonical`、`either` を追加できます。
boolean flag ではなく enum として query criteria に置きます。
`either` は event または session identity を key にした union を返し、同じ row を二重に返しません。
新しい selector は v0.30 の既定動作を変更しません。

producer と fixture の実装後は、`canonical_workspace`、`effective_workspace`、relationship 情報を additive な出力 field として公開できます。
unknown 値は省略するか `unknown` と表示し、偽の同値関係を返しません。

## Cross-host fixture

各 host fixture は canonical repository A と effective repository B を使います。
どの host でも、一つの session row が A を維持し、event が B に保存され、どちらの値も変更せずに conflict または review 済み alias を観測できることを確認します。

| Host | Canonical の証拠 | Event-local effective の証拠 | 必須 assertion |
|---|---|---|---|
| Codex | `SessionStart.cwd` から repository A を解決 | `PostToolUse.cwd` から repository B を解決 | audit event は B、session は A、再 delivery は同じ論理 event |
| Claude | `SessionStart.cwd` から A を解決 | `PostToolUse.tool_input` の working directory または hook `cwd` から B を解決 | command/tool event は B、event-local 証拠がない prompt は A へ fallback |
| Antigravity | 最初の `PreInvocation.workspacePaths` entry が A | `PreToolUse.toolCall.args.Cwd` が B で、`PostToolUse` と pair になる | pair 済み audit は B、後続 `PreInvocation` は session A を変更しない |
| Gemini CLI | `SessionStart.cwd` から A を解決 | `run_shell_command` の `AfterTool.cwd` から B を解決 | audit は B、新しい cwd がない `AfterAgent` は A を使う |
| Grok Build | session start の `cwd` から A を解決 | tool completion の `cwd` から B を解決 | audit は B、hook replay は冪等 |
| Kimi Code | `SessionStart.cwd` から A を解決 | `PostToolUse.cwd` から B を解決 | audit は B、`source=resume` は session A を維持 |

fixture は `presentation/cli/testdata` にある versioned かつ sanitized な host payload と、`docs/hooks/host-contract.json` の field inventory を使います。
host が特定の hook で event-local working-directory を送らない場合は、B を生成せず canonical fallback を確認します。

## 観測可能な振る舞いテスト

| Given | When | Then | Level |
|---|---|---|---|
| canonical A の新規 session | event が effective B を送る | session は A のままで event は B | application と SQLite integration |
| 既存 session A | resume/start retry が B を送る | canonical は A のままで observation に B を記録 | application behavior |
| canonical が local parent path | event が child path で発生 | relationship は `descendant` | domain table test |
| canonical が remote identity で effective が local checkout | review 済み alias がない | relationship は `conflict` | domain table test |
| review 済み alias が二つの値を関連付ける | alias で event が発生 | relationship は `explicit_alias` | application と SQLite integration |
| legacy DB row | additive migration を実行 | ID、外部キー、row 数、workspace column は不変 | migration test |
| B を指定した legacy event filter | session canonical は A で event effective は B | event を一度だけ返す | query integration |
| B を指定した legacy session filter | session canonical は A | 新しい scope を明示しない限り session を返さない | query integration |
| event-local workspace がない | event を記録 | effective workspace は canonical A へ fallback | host fixture |
| 両方の値が unknown | event を記録 | event を保存し、relationship は `unknown` | integration |

test は観測できる値と結果集合を保護します。
private helper の call order や、特定の host adapter 実装は固定しません。

## TDD と実装順

この設計 Issue では、この契約だけを ship します。
production change は既存の Issue 階層に従って分割します。

1. **#1435 ingest identity と host attribution**：immutable canonical selection、event-local effective selection、cross-host delivery retry の red test を追加し、domain/application attribution と host adapter を実装する。既定 query は変更しない。
2. **#1429 diagnostics と measurement**：observation table の migration と backfill を追加し、conflict/alias 診断と dry-run historical analysis を公開して、copy した local data で dogfood する。
3. **release QA #1437**：v0.30.0 の tagged release 前に、全 host fixture、migration copy test、filter compatibility test、conflict sample review を実行する。

各 behavior の red test は保存済み canonical/effective 値または public result membership を確認します。
最小の green 実装は既存の `workspace` column を再利用できます。
refactor では明示的な用語を導入し、core relationship rule を決定する処理を host handler から除去します。

## Structure review checkpoint

hook handler が canonical/effective の優先順位を所有する設計、usecase が複数の boolean で振る舞いを切り替える設計、SQLite DTO が domain rule に漏れる設計、private call order を固定する test は採用しません。
event recording の副作用で session canonical workspace が変わる設計と、生の証拠を保持する前に診断分類が primary event を拒否する設計も採用しません。

一つの attribution decision owner、小さい host adapter、失敗契約が観測できる repository transaction を使います。
alias は明示的な review 操作を必要とし、host adapter は heuristic に作成できません。

## Dogfood と release evidence

dogfooding は active store ではなく、copy した database を使います。
report には次の情報を残します。

- migration 前後の session row 数と event row 数。
- client/source hook ごとの exact、descendant、ancestor、explicit alias、conflict、unknown の件数と割合。
- event 本文を含まない conflict sample event ID。
- 対応する全 host fixture の false-positive review。
- 変更前後の既定 filter result 数。

既知 attribution の conflict が全体の 5% を超える場合、または特定 host の結果を fixture で説明できない場合は follow-up Issue を作成し、v0.30.0 の tagged release 前に実装します。
#1429 が要求する exact delivery duplicate rate は、これとは別に 1% 未満を目標とします。

## 対象外

- historical event の削除または統合。
- 本文の一致を identity または alias の証拠にする処理。
- session terminal state の変更。
- symlink の解決または remote provider への問い合わせによる workspace 正規化。
- legacy workspace filter の結果集合の拡大。
- workspace 診断への command、prompt、transcript 本文の保存。
