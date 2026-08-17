# 検索プロジェクションの再構築

[English](search-projection-rebuild.md)

オペレータ向けの **rebuild** は **search-index family** の再構築です（`traceary store compact --projection-rebuild` / `traceary doctor --fix` / `traceary store compact --projection-abort`）。コンパイル手順でも、ストア全体の rebuild でもありません。

検索プロジェクションは派生データです。正本のイベントとコマンド監査からいつでも再構築でき、プロジェクションのライフサイクル操作が正本を変更することはありません。v0.34 以降、complete な世代は `traceary search` に fingerprint pre-filter と session tier を提供します。本文一致は正本テーブルを新しい順に走査して復号する経路で判定し、再構築後に記録されたイベントも統合するため、再構築の合間に結果が古くなることはありません。

世代を一度も作っていない store でも、オペレータのコマンドは不要です。store を開くと generation 作業を上限付きで 1 単位進めます。idle かつ source event があるときだけ start し、それ以外は一致する rebuild を resume します。ただし代わりに skip される state があり、それらは後述の表に挙げています。この間も本文一致の検索は機能します。世代が `complete` でない状態は fingerprint による pre-filter が使えないことを意味するだけで、候補を直接復号して本文一致は正しく返ります。session tier は別です。世代が complete になるまで参照を拒否するため、session の要約やキーワードにだけ存在する一致はそれまで返りません。`traceary search` は stderr で session tier を参照しなかったことを通知し、`traceary doctor` を案内します。報告された state ごとに必要な操作は異なり、通常の store open では進まない state が 2 つあります。`failed` な generation には明示的な `start` が必要で、非既定の budget で開始された rebuild には同じ budget を指定した `resume` または `abort` が必要です。generation が rebuilding の間、`start` は拒否されるためです。旧世代の行を回収する前に、構築中の世代に対する session tier の実クエリが成功する必要があります。`status` が報告する前後の物理バイトは **bounded_search_projection** ファミリのみです。

オペレータは同じ機構を明示的に動かせます。`traceary store compact --projection-rebuild`で世代を開始します。parked または進行中の復旧は `traceary doctor --fix` で、必要なら置換世代を開始してから bounded batch を走らせます。

プロジェクションschemaより前のstoreをupgradeした場合、最初の`resume`バッチ群はpayloadをdecodeする前に、過去のevent identityをinventoryします。このphaseは`status`に明示され、安定したevent ID cursorを使用し、行数、保存バイト数、論理書き込みバイト数、wall time、lock timeの上限に従います。processを再起動すると最後にatomic commitされたcursorから再開します。過去行への並行の**update / delete**は、不完全なinventoryを受け入れずgenerationを無効化します。ライブの**insert**は無効化しません。events の insert trigger が新しい identity を `search_projection_source_sequence` へ無条件登録するため、inventory に追加作業はなく、store を開くたびに書く hook でも `complete` に到達できます。旧migration 38ですでに投入済みのstoreと新規の空storeは、正本tableをscanせずこのphaseを省略します。

世代が incomplete のまま残り、その budget の configuration hash が現在の既定と一致しない場合、store open 時の自動 catch-up はその budget を乗っ取らず skip します。skip は理由付きで warning レベルに記録されます。`resume` は同じ budget を再度指定したときだけ受け付けます。作業前に hash を比較するためです。その budget を再現できない場合は `abort` で世代を退役させ（`abort` は budget を取らず、行を `failed` / class `abandoned` にします）、そのうえで明示的な `start` で置き換えます。state が `rebuilding` の間、`start` 単独は拒否されます。

**failed** になった世代は自動で再起動せず、park します。この store が記録する failure class はいずれも決定的です。oversize な行はどの open でも同じ budget を超え、`session_tier_unverified` は同じクエリで失敗し、`abandoned` はオペレータの判断です。自動で作り直しても同じ失敗を繰り返し、open ごとに lifecycle 行が増えるだけです。自動 catch-up は class を明記した warning を出して skip します。`resume` は failed な世代を拒否し、`abort` は `abandoned` として failed のままにするため、どちらでも解除できません。復旧は `traceary doctor --fix` です（別 budget で作り直すときは `traceary store compact --projection-rebuild`）。

### state ごとに必要な操作

`traceary search` は `complete` 以外のすべての state で session tier を拒否します
（`infrastructure/sqlite/event_search_query.go:66-73`）。stderr の通知がコマンド名を挙げず
ここを案内するのは、必要なコマンドが state に依存し、そのうち `start` は世代が rebuilding の間
拒否されるためです。

budget hash に関する以下の行には、優先する条件が 1 つあります。`complete`、`rebuilding`、
`drifted` の世代が古い capacity semantics version で記録されている場合、budget hash に関係なく
次の store open で abandon され、置き換えられます
（`application/usecase/search_projection_usecase.go:315-340`）。そのため semantics の変更を
またいで upgrade した store は、budget hash の不一致からは自力で回復します。`failed` は意図的に
対象外で、park されたままです。解除すると決定的な失敗を open ごとに繰り返すためです。

| `status` の state | 次の通常の store open で起きること | 進んでいない場合 |
|---|---|---|
| `complete` | session tier が利用できます | — |
| `idle`（event あり） | 世代が start し、bounded batch が 1 回走ります | `start` のあと `resume` |
| `idle`（event なし） | 何も起きませんが、失われるものもありません。event のない store には一致する session がありません | — |
| `rebuilding` または `cleanup` 中の `drifted` で、budget hash が既定と一致 | bounded batch が 1 回 resume します | `resume --until-complete` |
| `rebuilding` または `cleanup` 中の `drifted` で、budget hash が不一致 | **skip** されます。世代は自力では完了しません | 開始時の budget を指定した `resume`。再現できない場合は `abort` のあと `start`。state が `rebuilding` の間 `start` 単独は拒否されますが、`drifted` では受け付けられます |
| `cleanup` 以外の `drifted` | 置き換えの世代が start します | `start` |
| `failed` | **skip** されます。意図的に park されています | `start`。`resume` も `abort` も解除しません |


cutover 前後の family バイト数は診断用の値であり、世代を start / complete する transaction の外側で、batch から切り離した context と専用の短い deadline のもとに測定します。測定できなかった場合でも世代が失敗することはありません。`status` は `cutover_before_evidence.status` / `cutover_after_evidence.status` を `unavailable` と理由付きで報告するため、0 バイトという値を「実際に空の family」と取り違えることはありません。before と after は測定時刻も対象 family の大きさも異なるため別々に持ちます。status が空文字の場合はまだ測定していないことを表します。

```sh
traceary doctor --fix
traceary store compact --projection-rebuild --lock-time 2s
```

各バッチには、行数、保存バイト数、デコード後バイト数、論理書き込みバイト数、ロック時間、バッチ実行時間の上限が引き続き適用されます。キャンセル時は最後にコミットしたチェックポイントが残るため、同じコマンドで再開できます。

未完了の世代を破棄して異なる設定で再開する場合は、`traceary store compact --projection-abort`を使います。この操作は冪等であり、完了済みのactive世代を破棄しません。世代の状態、チェックポイント、high-water、容量証跡は`traceary doctor`で確認します。

v0.34 以降、この projection が唯一の検索派生ファミリです。並走していた全文コーパス版 migration-032 ファミリは退役したため、認可すべき cutover も比較対象の第2インデックスも存在しません。詳細は [検索インデックスの退役](operations/search-retirement.ja.md) を参照してください。

## インデックスファミリ予算

オペレータ向けの予算は、有界検索インデックスファミリ（`search_projection_*` +
`literal_search_*`）の**物理バイト**です。単位は `dbstat` によるアクティブ
b-tree 割当であり、ソーステキストではありません。デフォルトは 1464 MiB
（約 1.43 GiB）です。

### この予算が実際に上限するもの

ファミリのうち**退避可能（evictable）**なのは 1 つのティアだけで、上限が効くのも
そこだけです。具体的には `search_projection_recent_documents`、その索引、
`search_projection_recent_fts_*` の各シャドウテーブルです。ここで eviction が成立するのは、recent ティアが保持上限だからです。最古のドキュメントを落とす
ことが現在安全なのは、このティアを読む処理がないためです。本文検索は正本の復号走査で
正しさを保ち、session tier は要約・キーワード一致を返す追加の lossy な面です。

残りのティアは**設計上コーパス比例**で、これを上限する仕組みはありません。

| ティア | 比例する対象 | evict できない理由 |
|---|---|---|
| `search_projection_session_summaries` / `_session_keywords` / `_command_aggregates` | セッション数 | セッション一致を返す追加の面。落とすとその一致を失う |
| `literal_search_fingerprints` | イベント数 | pre-filter が fail open するのは、その event の行が 1 件も無い場合だけ。行がある状態でクエリの要求する fingerprint が揃っていないと復号前に除外され、遅くなるのではなく偽陰性になる |
| `search_projection_source_sequence` / `_exclusions` | イベント数 | 再構築自体の台帳 |

これらは導出時に**非 recent 予備枠**として予算から先に差し引かれます。ファミリ合計に
対する計算はそれで正直になりますが、運用上の帰結があります。コーパスが育つと予備枠が
増え、上限は縮み、同じ予算でも recent ウィンドウは短くなります。リファレンスコーパス
では、1 回の走査の 36% 時点で、セッションティアとフィンガープリントだけで 1464 MiB
予算の 80.5% を占めていました。予備枠が予算に達すると導出上限は 0 になり、recent
ティアは空のまま構築されます。検索は正本の復号走査を使うため影響を受けません。これは
黙って隠されるのではなく報告されます（`capacity_evidence` に
`non-recent reserve at or above index-family budget`）。

`--index-family-bytes` を増やして買えるのは、現在はどこからも読まれない recent ティアの
保持量です。検索到達範囲を増やすわけでも、コーパス比例ティアを縮めるわけでもありません。

`status.recent_bytes` は意図的に**別単位**です。recent ティアに実際に保持された
ソーステキストです。予算はインデックスバイトで設定し、保持ソースは増幅率が
見えるように報告するもので、ノブをテキスト上限として読み替えるためのものではありません。

この予算が買うのは検索到達範囲ではなく、**可変の保持ウィンドウ**です。トライグラムは
ソーステキストの約 2.16 倍になるため、1464 MiB のファミリはインデックス可能な
テキスト約 0.66 GiB に相当します。参照コーパスの週次ボリュームは**8 倍**
（0.06〜0.47 GiB/週）振れます。中央値では約 1.5〜2 週、重いスプリントでは 1 週未満、
静かな週では 4〜5 週です。圧縮が買うのは**到達距離ではなく可逆性**です。
インデックスは平文の上に構築されるため、圧縮ボディも非圧縮と同じインデックス量を
占めます。session tier は追加の lossy な面であり、本文検索の到達範囲は正本の復号走査から
得られるため、この保持ウィンドウには bounded されません。

### 保証（Guarantee）

予算は**間接的に**強制されます。強制の実体は、測定はされているが推定値である
トライグラム増幅率から導出したソーステキスト上限です。eviction は recent ティアを
その上限に正確に収めます。結果としてファミリが実際に設定予算以下に収まったか
どうかは別の問題であり、事前に保証されるのではなく**測定して報告されます**。
世代完了時にファミリを再測定し、`index_family_within_budget` に `1`（以下）、
`0`（超過）、`-1`（測定不能）を記録します。

`-1` は**不明**を意味します。「予算以下」を意味することは決してありません。
新しい `doctor` InspectCapacity の dbstat cache があるとき、`doctor`
はその cache したファミリ合計（`physical_bytes`、`physical_evidence.status=cached`）
に対する粗い `0` / `1` を出します（persist はしません）。status 自身は dbstat を歩きません。
cache miss は即座に `unavailable` です。cutover は完了時に測定するので、complete 世代には
persist された判定が残ります。合計も取れないときだけ `-1` のまま、
`physical_evidence.reason` が理由を出します
（[#1835](https://github.com/duck8823/traceary/issues/1835)）。

超過として記録された世代はそのまま残ります。その場で是正する仕組みはありません。
次の `CatchUp` は完了済み世代を見て `already_complete` を返します。
`traceary doctor` は `index_family_within_budget` が `0` のとき
`search-projection-budget` を警告します。`traceary doctor`
でも同じ欄と `capacity_evidence` を確認してください。`0` の原因は 1 つではありません。そのコーパスに
対して増幅率の推定が低すぎた場合のほか、恒久常駐オブジェクトの増加
（`search_projection_source_sequence` はイベントごとに 1 行増え、回収されません）や、
削除済みドキュメントのページを FTS5 がまだマージしていない場合もあります。いずれの
原因でも対処は同じで、`--index-family-bytes` を小さくした明示的な
`traceary store compact --projection-rebuild` です。

この値は `dbstat` の割当でありファイルサイズではありません。ファイルは
`store compact` で縮み、FTS5 は削除ドキュメントの領域をセグメントマージ時にしか
返しません。

**再構築中は判定しません。** 測定自体は行われます（`Start` と eviction 突入後に
`dbstat` を走査します）。判定しないのは予算適合性のほうで、`Start` が新世代の検証まで
前世代を読める状態に残すため、再構築中は一時的に 2 ファミリを保持するからです。

eviction 時の再導出は永続化された義務です（`capacity_rederived`）。source→eviction
の commit と切り離した書き込みの間で crash しても、次の eviction apply で再試行します。
`dbstat` 分割、`SUM(decoded_bytes)`、reserve 照会は 1 つの読み取りスナップショットを
共有するので、並行する hook eviction が physical-before / logical-after を組むことは
ありません。再導出の前に FTS reclaim を走らせ、source 中の eviction が残した削除
posting を増幅率に混ぜません。比率は recent ティアに残っている全世代の混合のままです
（FTS ページは世代単位ではありません）。source の prefilter 走査は Start 時天井の 4 倍
slack を使うので、その範囲の上方修正では除外済みドキュメントを再投影しません。
[search-projection-capacity-derivation.ja.md](research/search-projection-capacity-derivation.ja.md)
を参照してください。

v0.34 以降、新世代が保持する**ソーステキスト**は構築中も上限されます。eviction は
source 走査と交互に実行されます。各バッチはその世代の累計ソーステキスト量を導出上限と
比較し、超えているか、カットオフより古い行があれば、追加投入の前に最古のドキュメントから
evict します。比較が読むのは永続化された `recent_source_bytes` カウンタなので、この
上限はプロセス再起動をまたいでも維持されます。v0.34 より前は最後のソース行を走査し
終えるまで eviction を開始できませんでした。408,893 イベントの store での実測では、
`recent_source_bytes` は走査中ずっと 690,432,000 の上限に対し 690,430,718 に張り付いて
いました。

このページの数値はすべて `modernc.org/sqlite v1.55.0` での実測値です。v0.34.0 が同梱
するのは v1.56.0 で、上流の journal rollback における corruption 修正を取り込むために
更新しました。この修正はストレージレイアウトにもスループットにも影響しません。数値は
「その engine で計測した値」としてそのまま報告し、出荷バイナリの正確な性質として
言い換えることはしません。

### 再構築ピークは上限されない

投入するソーステキストを上限しても、**インデックスバイトは上限されません**。差は
小さくありません。同じ store、`index_family_byte_limit` = 1.43 GiB での実測値です。

| 時点 | インデックスファミリ物理 | store ファイル |
|---|---|---|
| 前世代（complete） | 3.99 GB | 7.16 GB |
| source 走査の終了時 | 10.32 GB | 13.38 GB |
| cleanup バッチ 5 回後 | **>= 14.31 GB** | 17.42 GB |

この実行は `database or disk is full` で失敗し、旧世代のドキュメントが 83,209 件
未回収のまま終わりました。したがって 14.31 GB は**ピークではなくピークの下限**です。
増加の大半は `search_projection_recent_fts_data`（9.0 GB）でした。external-content の
FTS5 では削除が逆向きのエントリを追加し、セグメントマージが消すまで残ります。各バッチ後の
マージには上限があり、source フェーズではそもそも実行されません。この測定に使った
store の複製は、通常のアップグレードが保持する 2 世代ではなく 4 世代を抱えていたため、
値そのものはクリーンなアップグレードの数字ではありません。ただし形が測定条件の産物
というわけではありません。支配的なオブジェクトが何かに注意してください。
`search_projection_recent_fts` はどこからも読まれていなかったティアです（#1842）。
v0.37 で削除します。migration 066 が writer trigger を落とし、`store compact`
が work copy で virtual table を落とします。検索は decode walk のままです。
scratch: walk p50 は約 3.7–4.4 ms、比較 FTS は約 26–33 µs で速いですが、
読まれない 9 GB の元は取れません。
[search-projection-recent-fts.ja.md](research/search-projection-recent-fts.ja.md)
を参照。そのピークが買っていたのは保持量であり、検索結果ではありません。

**空き容量を設定予算から見積もらないでください。** 再構築は予算の数倍を必要とすることが
あり、容量不足で失敗することがあります。これが v0.37 のディスク約束です
（[#1753](https://github.com/duck8823/traceary/issues/1753)）。ピークは上限せず
**受け入れます**。再構築前回収は検索を止め、測定ピークを予算内に予約すると recent 窓が
空になり、第 2 の再構築天井フラグは足しません。scratch 測定（event 12 件）: gen1
family 258,048 → 再構築ピーク **405,504**（gen2 予算 225,280 の 1.80 倍）。`VACUUM`
なしではファイルサイズは動きません。
[search-projection-rebuild-peak.ja.md](research/search-projection-rebuild-peak.ja.md)
を参照。上の大規模コーパス下限は変わりません（[#1620](https://github.com/duck8823/traceary/issues/1620)）。

空き容量が厳しい場合、再構築には永続的な停止手段があります。`traceary store
search-projection abort` は世代を park し（`state=failed`、`failure_class=abandoned`）、
自動 catch-up は park された世代を再開しません。したがって増加はそこで止まり、明示的な
`traceary doctor --fix` を実行するまで止まったままです。検索は最後に
complete した世代（無ければ上限付きの復号スキャン）で動き続けます。

**世代をまたいだ合計**もこの予算では上限されません。前世代は完全に常駐したままです。
それが再構築中も検索に答えられる理由であり、回収されるのは終端の cleanup フェーズです。

`--recent-age`（既定 30 日）は残します。retention は
`created_at > max(ageCutoff, byteCutoff)` です。scratch 測定
（`TestRecentAgeBindingOnScratchCorpora`）:

| コーパス | byte cutoff | どちらが bind するか |
|---|---|---|
| 密な ingest（30 日以内に byte ceiling を超える） | 2026-06-20 | **byte** |
| 静かなストア（walk が ceiling に届かない） | 空 | **age** |

容量圧があるストアでは byte cutoff のほうが新しいので、`--recent-age` は窓を
さらに縮めません。age が bind するのは recent ティアがもともと小さいときだけです。
operator が `--recent-age` を触るのは、index-family 予算以外の理由で静かなストアの
古い行を落とす場合です。フラグ削除は admin の deprecation window が要ります
（N で notice、N+1 で削除）。
[search-projection-recent-age.ja.md](research/search-projection-recent-age.ja.md)
を参照してください。

session ティアは exact-keyword + `LIKE` のままです。unicode61+porter の FTS は
測定したうえで足しません（`TestSessionTierKeepsLikePathAndMeasuresPorterFTS`）。
`LIKE '%deploy%'` はすでに `deployed` に当たります。比較 index は短い要約
2,009 件で 290,816 バイト、family は 352,256 バイトで、recent 窓を決めるのと
同じ予算です。ラベル付き集合では LIKE の recall は悪くなく、FTS が勝つのは
substring の精度とダイアクリティカルだけです。予算外の第 2 index は足しません。
[search-projection-session-tier-index.ja.md](research/search-projection-session-tier-index.ja.md)
を参照してください。

source フェーズのカットオフは**構築コストの上限であり、強制機構ではありません**。
最新 20,000 行を走査します（壁時計タイムアウトではありません）。その単位は保存エンベロープのバイト数であり、
projection がインデックスする単位ではありません。`thinking` ブロックは走査には
数えられる一方でインデックス対象テキストからは除去されるため、推論の多い
コーパスでは過大に数えられます。そのため走査は導出上限の 4 倍に対して行い、
明らかに範囲外のものだけを除外して、正確な判断は eviction に委ねます。
最新 20,000 行が walk ceiling に届かないときは、空（age-only）ではなくその最古 timestamp
を cutoff にします。
[search-projection-recent-cutoff.ja.md](research/search-projection-recent-cutoff.ja.md)
を参照してください。
ここで除外したものはその世代では不可逆です。eviction はドキュメントを削除できますが、
再投影はできません。

恒久ティアだけで予算を使い切り導出上限が 0 になった場合、カットオフは recent ティアを
空にします。年齢ウィンドウ全体を構築してから全件 evict するのではなく、最初から
何も構築しません。予算に達している store で、保持ゼロのために最大の構築コストを
払うことがないようにするためです。

次の 3 つの数値を混同してはいけません。

1. **dbstat 割当** — ファミリに帰属するアクティブ b-tree ページ（予算の単位）
2. **`store compact` 後のファイルサイズ** — `VACUUM` 後にのみ縮む
3. **再構築時のディスクピーク** — cleanup が旧世代を回収するまで 2 世代が共存し、これを上限する設定値は存在しない（「再構築ピークは上限されない」を参照）
