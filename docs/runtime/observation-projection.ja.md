# Runtime 観測の投影

[English](observation-projection.md)

状態：純粋 reducer と既存 Runtime／selected 待ち行列への統合を実装済み。native host、スリープ／復帰、UI、再開の認定ではない。製品 Runtime に **native の現在状態を受け付ける経路は公開していない**。

## 責務と証拠

`domain/runtime.ObservationProjection` は epoch/run/generation/worktree/profile の完全な選択を所有する。プロセス、対話、元の捕捉の健全性、転送接続、履歴メタデータは別軸である。既存 application Runtime が実子プロセスと投影を所有し、新しい registry、worker、待ち行列、core Session event、永続 schema は追加しない。

既存 selected UDS schema／ACK は変更しない。`AdmitObservation` が公開済みの実行所有を確認し、**待ち行列に入れる前**に実所有者に束縛した opaque receipt を作る。`ConsumeObservation` は物理 registry の再検証と同一所有者の再確認をまたいで、元のローカル受付時刻を保持する。この receipt は native 出所、休止をまたぐ鮮度、権限、制御 handle の証明ではなく、client の field から decode できない。直接の `RecordObservation` も同じ受付／消費経路を使う。

hook の derived metadata は `ObserveUnbound` にだけ入る。source／版、元の境界種別、主張された native session／turn ID、元のローカル受付を持つ、定量上限付きの最終到着メタデータである。prompt／tool／stop／session 境界から working／waiting を推測せず、native 因果関係や Attention を作らない。未束縛の到着量は native 因果台帳を消費せず、既存の稼働中標本 `LastObservation` の更新を止めない。標本は終了／不確実化で消去し、履歴投影は別の表示として残す。

`NativeProjectionEvidence` は信頼済み adapter 専用の意味的 port であり、**wire DTO や認可 token ではない**。構文検証や主張された ID は証明にならない。native 現在状態を有効にする前に、版別に認定した host adapter が native source、完全な binding、event identity／順序、元の受付、鮮度を独立検証し、**同じ元の #2464 ObservationStamp** で commit と現在状態の read の両方を guard し、native gate の失効を投影へ伝える必要がある。古い queued／unbound metadata に新しい stamp を後付けしてはならない。これらの条件と実 host の event rate／上限の認定は host leaf で引き続き必須であり、合成 test はその証明にならない。

## 因果関係、有効期限、履歴

- native sequence は検証済み host 順序であり、receipt sequence や host 壁時計順序ではない。新しい証拠は固定した source／native 会話とローカル継続性 revision に一致する必要がある。不明な predecessor、同順序の衝突、identity 不整合は現在知識を unknown にする。
- predecessor が不明でも検証済みの最大 native frontier は保持する。最初に A より B が来た場合は unknown のままとし、遅れて来た古い A は現在状態を復活させない。正しく連なる**新しい鮮度付き C**だけが回復できる。保存した B を A の後で再生／昇格しない。
- native 重複 ID は最初に受理した単調時計の受付を保持する。失効前後の重複で対話を延長／復活したり Attention を出したりしない。native 専用の生涯台帳は hashed identity／内容指紋を最大 256 件保持し、古い ID を追い出して replay を新規扱いしない。枯渇は native 現在状態を unknown にし、native port の呼び出し元に上限 error を返すが、**捕捉／selected 転送の劣化判定には使わない**。通常の未束縛メタデータは継続する。native 有効化前に rate／上限の意味を認定する。
- working と waiting の両方を、最初の非重複受理の元の受付から **60 秒**で失効させる。dequeue／commit 時刻から測らない。任意の host 壁時計は表示用のみである。純粋な失効は read／変更時に評価し、単調時計だけでは OS 休止をまたぐ鮮度を証明しない。
- 明示的な sleep／wake／gap／restart／時計の無効化は継続性 revision を進め、重複台帳と履歴を保持する。因果衝突は revision を進めず現在知識だけを unknown にする。独立検証済みで正しく連なる新しい証拠なら回復できる。native 会話をその場で reset してはならず、同じ会話の検証済み再開／新たな実所有は #2478 が担当する。
- stop はプロセス終了、readiness、安全な貼付の証拠ではない。approval telemetry は見えている人間待ち、権限、承認ではない。実際の process state／publication uncertainty は元の所有者から取得する。application の不確実化は投影を失効させ、process enum が live でも以後の受付を拒否する。
- 最後の waiting は元の受付／表示時刻と stale flag を保持し、現在の対話から分離する。Attention は waiting への受理した遷移を識別し、反復 waiting、重複到着、OS 通知、権限ではない。revision は受理した snapshot の版を表す。拒否した信頼済み証拠も、定量上限付きの重複台帳／最終到着履歴に残り得るが、履歴は現在状態の証明にならない。

## Snapshot の購読

`SubscribeObservations` は実際の不変 RunSnapshot、独立した ProjectionSnapshot、既存の共有 change channel を所有者 lock 内で原子的に返す。起床の取りこぼし区間、client 別待ち行列、新しい worker は作らない。観測通知は **launch 一覧 revision を増やさない**ため、telemetry が一覧の継続ページ取得を無効化しない。

これは最新 snapshot の購読であり、**exactly-once の event／Attention stream ではない**。完全な選択と投影 Version を比較し、版が飛んだ場合は中間 snapshot が欠けた可能性を扱う。無関係な所有／制御操作でも起床し得る。**失効時の channel 起床は保証しない**。read 側は Changed を待つことに加え、正の `RefreshAfter` 以内に再読を予約する必要がある。native 現在状態の read には前述の元の継続性 gate も必須である。

## 検証と rollback

偽の epoch 単調時計で両 TTL、元の受付からの遅延、失効前後の重複、native 順序／順列、最初の predecessor 欠落、衝突、foreign binding／source／session、未来受付、host 時刻の非権威性、restart／wake／gap、process／health の分離、最後の waiting、Attention を検証する。Runtime test は opaque 所有者 receipt、元の timestamp、原子的購読通知、観測を挟んだ一覧継続、256 件を超える未束縛到着、実終了後の journal 不確実化を検証する。実 selected UDS／有限所有 PTY fixture は製品の未束縛経路と metadata digest の不変性を検証するが、native host の認定ではない。

rollback は投影／Attention を停止し、unknown と既存履歴 metadata を返す。履歴削除、core Session 意味の変更、入力 replay、保存 PID の reset／adopt、host 設定の書換え、暗黙 agent 再開を行ってはならない。物理 sleep／wake 検証は明示的に後回しとした **NOT_RUN** であり、合格ではない。
