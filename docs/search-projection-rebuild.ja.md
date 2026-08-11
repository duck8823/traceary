# 検索プロジェクションの再構築

[English](search-projection-rebuild.md)

検索プロジェクションは派生データです。正本のイベントとコマンド監査からいつでも再構築でき、プロジェクションのライフサイクル操作が正本を変更することはありません。v0.34 以降、complete な世代は `traceary search` に fingerprint pre-filter と session tier を提供します。本文一致は正本テーブルを新しい順に走査して復号する経路で判定し、再構築後に記録されたイベントも統合するため、再構築の合間に結果が古くなることはありません。

世代を一度も作っていない store でも、オペレータのコマンドは不要です。store を開くたびに generation 作業を上限付きで 1 単位進めます。idle かつ source event があるときだけ start し、それ以外は一致する rebuild を resume します。この間も本文一致の検索は機能します。世代が `complete` でない状態は fingerprint による pre-filter が使えないことを意味するだけで、候補を直接復号して本文一致は正しく返ります。session tier は別です。世代が complete になるまで参照を拒否するため、session の要約やキーワードにだけ存在する一致はそれまで返りません。`traceary search` はこれを伝えません。MCP の `search` tool だけが `session_projection_not_ready` として報告します（#1844）。旧世代の行を回収する前に、構築中の世代に対する session tier の実クエリが成功する必要があります。`status` が報告する前後の物理バイトは **bounded_search_projection** ファミリのみです。

オペレータは同じ機構を明示的に動かせます。`traceary store search-projection start`で世代を開始します。`resume`は上限付きバッチを1回実行します。複数のバッチを個別にコミットしながら実行する例を次に示します。

プロジェクションschemaより前のstoreをupgradeした場合、最初の`resume`バッチ群はpayloadをdecodeする前に、過去のevent identityをinventoryします。このphaseは`status`に明示され、安定したevent ID cursorを使用し、行数、保存バイト数、論理書き込みバイト数、wall time、lock timeの上限に従います。processを再起動すると最後にatomic commitされたcursorから再開します。過去行への並行の**update / delete**は、不完全なinventoryを受け入れずgenerationを無効化します。ライブの**insert**は無効化しません。events の insert trigger が新しい identity を `search_projection_source_sequence` へ無条件登録するため、inventory に追加作業はなく、store を開くたびに書く hook でも `complete` に到達できます。旧migration 38ですでに投入済みのstoreと新規の空storeは、正本tableをscanせずこのphaseを省略します。

オペレータが非デフォルトの budget で世代を開始したまま中断した場合、store open 時の自動 catch-up はその budget を乗っ取らず skip します。skip は理由付きで warning レベルに記録されます。進捗を再開するには、一致する budget で resume するか abort してください。

**failed** になった世代は自動で再起動せず、park します。この store が記録する failure class はいずれも決定的です。oversize な行はどの open でも同じ budget を超え、`session_tier_unverified` は同じクエリで失敗し、`abandoned` はオペレータの判断です。自動で作り直しても同じ失敗を繰り返し、open ごとに lifecycle 行が増えるだけです。自動 catch-up は class を明記した warning を出して skip します。`resume` は failed な世代を拒否し、`abort` は `abandoned` として failed のままにするため、どちらでも解除できません。復旧は明示的な `traceary store search-projection start` です。

cutover 前後の family バイト数は診断用の値であり、世代を start / complete する transaction の外側で、batch から切り離した context と専用の短い deadline のもとに測定します。測定できなかった場合でも世代が失敗することはありません。`status` は `cutover_before_evidence.status` / `cutover_after_evidence.status` を `unavailable` と理由付きで報告するため、0 バイトという値を「実際に空の family」と取り違えることはありません。before と after は測定時刻も対象 family の大きさも異なるため別々に持ちます。status が空文字の場合はまだ測定していないことを表します。

```sh
traceary store search-projection resume --until-complete --max-batches 4000 --total-wall-time 8h
```

各バッチには、行数、保存バイト数、デコード後バイト数、論理書き込みバイト数、ロック時間、バッチ実行時間の上限が引き続き適用されます。キャンセル時は最後にコミットしたチェックポイントが残るため、同じコマンドで再開できます。

未完了の世代を破棄して異なる設定で再開する場合は、`traceary store search-projection abort`を使います。この操作は冪等であり、完了済みのactive世代を破棄しません。世代の状態、チェックポイント、high-water、容量証跡は`status`で確認します。

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

`-1` は**不明**を意味します。「予算以下」を意味することは決してありません。大きい
store では構築中ずっとこの値になります。判定に必要な `dbstat` 走査には期限があり、
数 GB の store はその中に収まらないため、`capacity_evidence` は `unavailable` を
報告し、判定は生成されません（408,893 イベントの再構築 118 サンプル全件で実測）。
同じ出力の `physical_bytes` は測定できるため、判定が出なくてもファミリのサイズ自体は
観測できます（[#1835](https://github.com/duck8823/traceary/issues/1835)）。

超過として記録された世代はそのまま残ります。その場で是正する仕組みはありません。
次の `CatchUp` は完了済み世代を見て `already_complete` を返します。
`traceary store search-projection status` で `index_family_within_budget` と
`capacity_evidence` を確認してください。`0` の原因は 1 つではありません。そのコーパスに
対して増幅率の推定が低すぎた場合のほか、恒久常駐オブジェクトの増加
（`search_projection_source_sequence` はイベントごとに 1 行増え、回収されません）や、
削除済みドキュメントのページを FTS5 がまだマージしていない場合もあります。いずれの
原因でも対処は同じで、`--index-family-bytes` を小さくした明示的な
`traceary store search-projection start` です。

この値は `dbstat` の割当でありファイルサイズではありません。ファイルは
`store compact` で縮み、FTS5 は削除ドキュメントの領域をセグメントマージ時にしか
返しません。

**再構築中は判定しません。** 測定自体は行われます（`Start` と source→eviction 遷移で
`dbstat` を走査します）。判定しないのは予算適合性のほうで、`Start` が新世代の検証まで
前世代を読める状態に残すため、再構築中は一時的に 2 ファミリを保持するからです。

v0.34 以降、新世代が保持する**ソーステキスト**は構築中も上限されます。eviction は
source 走査と交互に実行されます。各バッチはその世代の累計ソーステキスト量を導出上限と
比較し、超えているか、カットオフより古い行があれば、追加投入の前に最古のドキュメントから
evict します。比較が読むのは永続化された `recent_source_bytes` カウンタなので、この
上限はプロセス再起動をまたいでも維持されます。v0.34 より前は最後のソース行を走査し
終えるまで eviction を開始できませんでした。408,893 イベントの store での実測では、
`recent_source_bytes` は走査中ずっと 690,432,000 の上限に対し 690,430,718 に張り付いて
いました。

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
`search_projection_recent_fts` はどこからも読まれていないティア（#1842）であり、
このピークが消費するディスクは現在、検索結果ではなく保持量のために使われています。

**空き容量を設定予算から見積もらないでください。** 再構築は予算の数倍を必要とすることが
あり、容量不足で失敗することがあります。詳細は
[#1620](https://github.com/duck8823/traceary/issues/1620)、ディスク容量の約束そのものは
[#1753](https://github.com/duck8823/traceary/issues/1753) で継続作業中です。

空き容量が厳しい場合、再構築には永続的な停止手段があります。`traceary store
search-projection abort` は世代を park し（`state=failed`、`failure_class=abandoned`）、
自動 catch-up は park された世代を再開しません。したがって増加はそこで止まり、明示的な
`traceary store search-projection start` を実行するまで止まったままです。検索は最後に
complete した世代（無ければ上限付きの復号スキャン）で動き続けます。

**世代をまたいだ合計**もこの予算では上限されません。前世代は完全に常駐したままです。
それが再構築中も検索に答えられる理由であり、回収されるのは終端の cleanup フェーズです。

source フェーズのカットオフは**構築コストの上限であり、強制機構ではありません**。
コーパスを新しい順に走査しますが、その単位は保存エンベロープのバイト数であり、
projection がインデックスする単位ではありません。`thinking` ブロックは走査には
数えられる一方でインデックス対象テキストからは除去されるため、推論の多い
コーパスでは過大に数えられます。そのため走査は導出上限の 4 倍に対して行い、
明らかに範囲外のものだけを除外して、正確な判断は eviction に委ねます。
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
