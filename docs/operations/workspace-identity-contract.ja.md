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

最初に session row を正常に作成できた時点で canonical workspace を固定します。
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
workspace などの attribution field は delivery fingerprint から除外します。
そのため、retry で attribution が変わっても、一つの論理 start を identity conflict にはしません。
新しい attribution fingerprint である場合だけ、追加の supplemental observation を記録します。

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
insert 後の event workspace は変更しません。
retry で得た attribution は supplemental observation だけに維持し、元の event を書き換えません。

boundary event は、host が明確な boundary-local workspace を送らない限り session canonical workspace を使います。
どちらの場合も session row は変更しません。

### Relationship の分類

relationship の分類は診断情報であり、event の保存を拒否する条件にはしません。
次の順に一つの分類を決めます。

1. `unknown`：どちらかの値が空。
2. `exact`：正規化した値が一致。
3. `explicit_alias`：operator が review した alias が二つの値を関連付けている。
4. `descendant`：両方が絶対 local path であり、effective path が canonical path の配下。
5. `ancestor`：両方が絶対 local path であり、effective path が canonical path を内包。
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

生の workspace attribution は、best-effort の診断情報ではなく必須の provenance です。
workspace を持つ event を作る成功経路では、必ず event と primary workspace observation を同じ transaction で commit します。
schema、I/O、constraint の実エラーでは、完全な provenance があるように見える event を残さず transaction を失敗させます。
delivery identity が完全に一致する retry は冪等な成功として扱い、transaction を失敗させず、二つ目の event は追加しません。
attribution が同じ場合は observation も追加せず、attribution fingerprint が新しい場合だけ一つの supplemental observation を追加します。

relationship の分類と集計 report は派生診断です。
任意の証拠が malformed で分類できない場合は、生の observation を維持し、`unknown` と `diagnostic_reason` を記録します。
この場合、有効な event の保存は拒否しません。
二次的な集計 report の更新失敗も、primary event と生の provenance の保存を妨げてはいけません。

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

CREATE TABLE hook_deliveries (
    delivery_record_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    reported_delivery_id TEXT NOT NULL,
    delivery_fingerprint TEXT NOT NULL,
    identity_status TEXT NOT NULL
        CHECK (identity_status IN ('accepted', 'conflict')),
    observed_event_id TEXT NOT NULL,
    accepted_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT '',
    UNIQUE (session_id, reported_delivery_id, delivery_fingerprint)
);

CREATE UNIQUE INDEX idx_hook_deliveries_accepted_identity
    ON hook_deliveries(session_id, reported_delivery_id)
    WHERE identity_status = 'accepted';

CREATE TABLE hook_delivery_attempts (
    delivery_record_id TEXT NOT NULL,
    attempted_event_id TEXT NOT NULL,
    outcome TEXT NOT NULL
        CHECK (outcome IN ('accepted', 'conflict', 'exact_redelivery')),
    attempt_origin TEXT NOT NULL
        CHECK (attempt_origin IN ('runtime', 'backfill')),
    observed_at TEXT NOT NULL,
    PRIMARY KEY (delivery_record_id, attempted_event_id)
);

CREATE TABLE session_workspace_observations (
    observation_id TEXT PRIMARY KEY,
    session_id TEXT NOT NULL,
    workspace TEXT NOT NULL,
    raw_workspace TEXT,
    observation_kind TEXT NOT NULL
        CHECK (observation_kind IN ('primary', 'supplemental')),
    observation_origin TEXT NOT NULL
        CHECK (observation_origin IN ('runtime', 'backfill')),
    observed_relationship TEXT NOT NULL
        CHECK (observed_relationship IN ('exact', 'descendant', 'ancestor', 'explicit_alias', 'conflict', 'unknown')),
    observed_event_id TEXT,
    delivery_record_id TEXT,
    attribution_fingerprint TEXT NOT NULL,
    diagnostic_reason TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,
    source_client TEXT NOT NULL DEFAULT '',
    source_hook TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_session_workspace_observations_relationship
    ON session_workspace_observations(observed_relationship, observed_at DESC, session_id);

CREATE UNIQUE INDEX idx_session_workspace_observations_delivery_attribution
    ON session_workspace_observations(delivery_record_id, attribution_fingerprint)
    WHERE delivery_record_id IS NOT NULL AND delivery_record_id <> '';

CREATE UNIQUE INDEX idx_session_workspace_observations_primary_event
    ON session_workspace_observations(observed_event_id)
    WHERE observation_kind = 'primary'
      AND observed_event_id IS NOT NULL AND observed_event_id <> '';
```

これらの table は attribution metadata だけを保存します。
command input、output、prompt text、transcript text、その他の event 本文を複製しません。
report は observation row から件数を計算しますが、件数は deduplication の判定ではありません。

alias は一つの session に限定し、reviewer と timestamp を持つ review 操作でだけ追加します。
alias を追加しても過去の `observed_relationship` は書き換えません。
report は ingest 時に観測した relationship と、review 済み alias を join して求めた現在の relationship を両方表示できます。

両方の provenance table にある `observed_event_id` は外部キーではなく、immutable な値です。
event を削除または archive しても append-only の delivery または workspace provenance は変更せず、report は left join で現在の event の有無を確認します。

受理する reported delivery identity の namespace は `<client>:<hook-kind>:<native-session-id>:<native-delivery-id>`、または同等の型付き表現とします。
host adapter は一つの論理 delivery に対して値を安定させ、hook kind または session ID をまたいで native ID を再利用してはいけません。
host が安定した native ID を提供しない場合は `hook_deliveries` row を作らず、本文が同じ delivery も別の正当な event として扱います。

delivery fingerprint は、hook kind と本文がある場合の digest を含む、正規化した semantic delivery envelope を対象にします。
retry で正当に改善できる workspace などの attribution field は除外します。
本文の一致は安定した reported identity の検証にだけ使い、本文だけを identity または deduplication の証拠にしません。
別の attribution fingerprint は、正規化した workspace、生の workspace、その他の安定した attribution input を対象にします。

repository は reported identity を次の順に処理します。

1. `(session_id, reported_delivery_id, delivery_fingerprint)` が既に存在する場合は、同じ論理 delivery とする。event は追加せず、`(delivery_record_id, attribution_fingerprint)` が新しい場合だけ supplemental observation を追加する。それ以外は完全に冪等な成功を返す。
2. reported identity の accepted row がない場合は、`accepted` delivery row、event、一つの `primary` observation を atomic に追加する。
3. 別の delivery fingerprint を持つ accepted row がある場合は、新しい fingerprint を key に `conflict` delivery row、新しい正当な event、primary observation を atomic に追加し、`diagnostic_reason=delivery_identity_conflict` を記録する。

delivery table の triple uniqueness により、保存済み conflict delivery の retry は手順 1 で解決し、event を増幅させません。
partial accepted-identity index は最初に受理した fingerprint を識別します。
冪等性はこの index だけではなく、application の比較と完全な ledger で実現します。
primary-event index は保存済み event ごとに primary attribution を一つに制限し、supplemental observation は同じ event に対する改善済み retry attribution を維持できます。

同時 delivery の処理も repository 契約に含めます。
最初の lookup 後に accepted identity または triple identity の unique collision が起きた場合、repository はその write を rollback し、新しい transaction で append-only ledger を再読込して判定を一度だけやり直します。

- 同じ fingerprint は手順 1 の冪等な成功に進む。
- 異なる fingerprint は手順 3 に進み、一つの conflict event を維持する。
- conflict の insert 中に triple identity が衝突した場合は、作成済み conflict row を再読込して手順 1 に進む。

各 collision で許す再読込は一度です。
一つの delivery が判定へ戻る回数は最大二度、accepted-identity race の後と conflict-triple race の後に一度ずつであり、上限のない retry loop にはしません。
この分岐で扱うのは二つの identity unique collision だけです。
busy timeout、check violation、schema error、I/O failure は実 transaction error のままにします。
observation の `session_id` は意図的に外部キーにしません。
historical event と direct event write は materialize された `sessions` row がない session ID を持つ可能性があり、migration はその row を維持する必要があるためです。
該当する observation は session row が作られるまで `unknown` とし、review 済み alias の追加には外部キーによって実在する session を要求します。

schema migration は、短い transaction で additive table と index だけを作成します。
schema transaction を保持したまま全 event を scan しません。
その後、再開可能な catch-up worker が、安定した `(created_at, id)` の keyset pagination と `backfill:<event-id>` observation に対する `INSERT ... ON CONFLICT DO NOTHING` を使い、primary observation がない event を write transaction ごとに最大 1,000 件処理します。
batch size は migration test と制約のある環境向けに変更可能にします。
database initialization は schema transaction の完了後、catch-up が全件を補完する前でも成功できます。
catch-up は上限付き batch の間で処理を譲り、coverage が未完了でも通常の ingest を妨げません。
未完了状態は diagnostics にだけ表示します。

catch-up は次の処理を行います。

1. すべての `sessions` row と `events` row を変更しない。
2. primary observation がない event ごとに `observation_origin=backfill` の primary observation を一つ追加し、`observation_id` には `backfill:<event_id>` を使う。primary observation または backfill ID が既に存在する場合は、冪等な補完済み結果とする。
3. event timestamp と event ID を `observed_at` と `observed_event_id` に設定する。
4. 過去 row は安定した delivery identity を保持していないため delivery-ledger row を作らず、`raw_workspace` は unknown のままにする。
5. `sessions.workspace` と比較し、exact と local path の ancestor/descendant rule だけで分類する。
6. 既知の不一致 pair は `conflict` とし、session row またはいずれかの workspace が不明な場合だけ `unknown` にする。
7. alias row を自動作成せず、本文の一致を alias または delivery の証拠にしない。

backfill の source client と source hook は、利用できる場合に各 event から取得します。
upgrade 後の初期化では毎回、observation が欠けている event の catch-up を再開します。
これにより以前の binary へ rollback している間に追加された event も、次の upgrade で補完します。
diagnostics は catch-up coverage を表示し、欠落件数が 0 になるまで historical measurement が完了したとは表示しません。

実装では、空 workspace、materialize された session row がない event、複数 effective workspace にまたがる session、Windows path、exact redelivery の identity 比較、変更された retry attribution、identity-conflict delivery の再送、review 済み alias、event 削除後も不変な observed event ID、上限付き batch commit、中断した catch-up、migration 後の冪等な再 open をテストします。

### Rollback

通常の rollback では以前の binary を deploy し、additive delivery table、observation table、alias table は維持したまま無視します。
以前の binary は変更されていない `sessions.workspace` と `events.workspace` を読み書きできます。
down migration は workspace column を書き換えず、event を削除しません。
以前の binary が書いた event には一時的な observation の欠落が生じますが、再 upgrade 後の catch-up で補完します。
runtime だけが持つ生 attribution と delivery provenance は event から再構築できないため、additive table の削除は rollback 操作に含めません。
将来これらを破棄する場合は、rollback 中の event と export 済み provenance の両方を維持する、別途 review した export-and-merge migration を必要とします。
database backup の restore だけでは不十分です。

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

### Handoff と context pack の結果集合

v0.30 の handoff resolver は、任意の session ID `S`、要求 workspace `W`、`active_only` に対して次の契約を維持します。

1. `S`、canonical workspace が `W` と完全一致、`active_only` を条件に session を問い合わせ、`started_at DESC, session_id DESC` の先頭 row を選ぶ。
2. 完全一致がなく、`W` が絶対 local path の場合だけ、最も近い parent から filesystem root まで ancestor をたどる。
3. 各 ancestor `A` について、canonical workspace が `A` と完全一致する session だけを `started_at DESC, session_id DESC` で 50 件ずつ調べ、candidate と同じ session ID かつ effective workspace が `W` と完全一致する event を一つ以上持つ最初の candidate を選ぶ。
4. `S` を指定した場合と `active_only` は fallback 中も filter として維持する。remote identity では ancestor fallback を行わない。
5. 条件を満たす candidate がなければ、matching session なしを返す。

したがって canonical workspace の完全一致は、effective-workspace evidence を持つ新しい ancestor session より優先します。
選んだ session ID が handoff の session metadata、recent command item、compact summary、その他の event section を決めます。
これらの section は `W` で再 filter せず、その session に含まれるすべての effective workspace の event を含められます。
memory candidate は、選んだ canonical workspace、異なる場合の要求 `W`、選んだ session family、有効な selected-session agent の順に、重複を除いた scope を使います。
context pack は選んだ canonical workspace を公開し、`W` は requested-workspace の match note として維持します。

integration test では、選ばれた session ID、candidate の順序、event section の結果集合、memory scope の順序、完全一致の優先、session ID と active filter、該当なし、remote fallback を行わないことを固定します。

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

1. **#1435 schema-first ingest identity と host attribution**：最初に additive schema を追加する。その後、immutable canonical selection、event-local effective selection、exact retry の比較、identity conflict の維持、cross-host delivery retry の red test を追加する。domain/application attribution、event と observation の atomic write、再開可能な catch-up、host adapter を実装し、既定 query は変更しない。
2. **#1429 diagnostics と measurement**：conflict/alias 診断、catch-up coverage、dry-run historical analysis を公開し、copy した local data で dogfood する。runtime writer より後で schema を追加せず、#1435 の observation schema を利用する。
3. **release QA #1437**：v0.30.0 の tagged release 前に、全 host fixture、migration copy test、filter compatibility test、conflict sample review を実行する。

各 behavior の red test は保存済み canonical/effective 値または public result membership を確認します。
最小の green 実装は既存の `workspace` column を再利用できます。
refactor では明示的な用語を導入し、core relationship rule を決定する処理を host handler から除去します。

## Structure review checkpoint

hook handler が canonical/effective の優先順位を所有する設計、usecase が複数の boolean で振る舞いを切り替える設計、SQLite DTO が domain rule に漏れる設計、private call order を固定する test は採用しません。
event recording の副作用で session canonical workspace が変わる設計、派生分類が有効な event を拒否する設計、必須の生 attribution を atomic に維持せず event が完全な provenance を持つように見せる設計も採用しません。

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
