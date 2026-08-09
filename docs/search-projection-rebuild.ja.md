# 検索プロジェクションの再構築

[English](search-projection-rebuild.md)

検索プロジェクションは派生データです。正本のイベントとコマンド監査からいつでも再構築でき、プロジェクションのライフサイクル操作が正本を変更することはありません。v0.34 以降、世代が complete のときは `traceary search` がこのプロジェクションを読みます。再構築後に記録されたイベントは正本テーブルから統合するため、再構築の合間に結果が古くなることはありません。

世代を一度も作っていない store でも、オペレータのコマンドは不要です。store を開くたびに generation 作業を上限付きで 1 単位進めます。idle かつ source event があるときだけ start し、それ以外は一致する rebuild を resume します。この間も検索は機能します。世代が `complete` でない状態は fingerprint による pre-filter が使えないことを意味するだけで、候補を直接復号して正しい結果を返します。旧世代の行を回収する前に、構築中の世代に対する session tier の実クエリが成功する必要があります。`status` が報告する前後の物理バイトは **bounded_search_projection** ファミリのみです。

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

v0.34 以降、この projection が唯一の検索インデックスです。並走していた全文コーパス版 migration-032 ファミリは退役したため、認可すべき cutover も比較対象の第2インデックスも存在しません。詳細は [検索インデックスの退役](operations/search-retirement.ja.md) を参照してください。

## インデックスファミリ予算

オペレータ向けの予算は、有界検索インデックスファミリ（`search_projection_*` +
`literal_search_*`）の**物理バイト**です。単位は `dbstat` によるアクティブ
b-tree 割当であり、ソーステキストではありません。デフォルトは 1464 MiB
（約 1.43 GiB）で、4 GiB ストアゲートから Wave 3 の他の削除分を差し引いた残余です。

`status.recent_bytes` は意図的に**別単位**です。recent ティアに実際に保持された
ソーステキストです。予算はインデックスバイトで設定し、保持ソースは増幅率が
見えるように報告するもので、ノブをテキスト上限として読み替えるためのものではありません。

この予算が買うのは固定ウィンドウではなく、**可変ウィンドウ**です。トライグラムは
ソーステキストの約 2.16 倍になるため、1464 MiB のファミリはインデックス可能な
テキスト約 0.66 GiB に相当します。参照コーパスの週次ボリュームは**8 倍**
（0.06〜0.47 GiB/週）振れます。中央値では約 1.5〜2 週、重いスプリントでは 1 週未満、
静かな週では 4〜5 週です。圧縮が買うのは**到達距離ではなく可逆性**です。
インデックスは平文の上に構築されるため、圧縮ボディも非圧縮と同じインデックス量を
占めます。ウィンドウより古いものは session ティア経由で到達できます。

### 保証（Guarantee）

予算は**間接的に**強制されます。強制の実体は、測定はされているが推定値である
トライグラム増幅率から導出したソーステキスト上限です。eviction は recent ティアを
その上限に正確に収めます。結果としてファミリが実際に設定予算以下に収まったか
どうかは別の問題であり、事前に保証されるのではなく**測定して報告されます**。
世代完了時にファミリを再測定し、`index_family_within_budget` に `1`（以下）、
`0`（超過）、`-1`（測定不能）を記録します。

超過として記録された世代はそのまま残ります。その場で是正する仕組みはありません。
次の `CatchUp` は完了済み世代を見て `already_complete` を返します。
`traceary store search-projection status` で `index_family_within_budget` と
`capacity_evidence` を確認してください。`0` はそのコーパスに対して増幅率の推定が
低すぎたことを意味し、対処は `--index-family-bytes` を小さくした明示的な
`traceary store search-projection start` です。

この値は `dbstat` の割当でありファイルサイズではありません。ファイルは
`store compact` で縮み、FTS5 は削除ドキュメントの領域をセグメントマージ時にしか
返しません。

**再構築中は測定されません。** `Start` は新世代が検証されるまで前世代を読める
状態に残すため、再構築中は一時的に 2 ファミリを保持します。source フェーズの
カットオフは新世代が年齢ウィンドウ全体で構築されるのを抑えますが、一時的な
ピークはこの予算では上限されません。

source フェーズのカットオフは**構築コストの上限であり、強制機構ではありません**。
コーパスを新しい順に走査しますが、その単位は保存エンベロープのバイト数であり、
projection がインデックスする単位ではありません。`thinking` ブロックは走査には
数えられる一方でインデックス対象テキストからは除去されるため、推論の多い
コーパスでは過大に数えられます。そのため走査は導出上限の 4 倍に対して行い、
明らかに範囲外のものだけを除外して、正確な判断は eviction に委ねます。
ここで除外したものはその世代では不可逆です。eviction はドキュメントを削除できますが、
再投影はできません。

次の 3 つの数値を混同してはいけません。

1. **dbstat 割当** — ファミリに帰属するアクティブ b-tree ページ（予算の単位）
2. **`store compact` 後のファイルサイズ** — `VACUUM` 後にのみ縮む
3. **再構築時のディスクピーク** — cleanup が旧世代を回収するまで 2 世代が共存する
