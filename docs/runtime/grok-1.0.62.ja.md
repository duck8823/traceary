# Grok Build native 認定: 1.0.62

[English](grok-1.0.62.md)

**承認済みの delivery 分離（2026-11-05）:** 安全な [実装・証跡 #2477](https://github.com/duck8823/traceary/issues/2477) と [未完了の native 認定 #2497](https://github.com/duck8823/traceary/issues/2497) は別です。実装の merge を native binding・状態・復旧の合格にせず、未認定機能は fail-closed/unknown のままです。

**状態：調査のみ。native AI turn、callback、安全な復旧は未認定です。** [#2477](https://github.com/duck8823/traceary/issues/2477) を追跡し、親を完了扱いにしません。
[本文を含まない metadata](evidence/grok-1.0.62.json) で実測と未実施 gate を区別します。

## 実測 source

導入済みの直接実行する arm64 binary は `grok 1.0.62 (1474f9cbdd91)` と報告し、SHA256 は `ec434e831afdcfce87780f2815fd732b1df8a4a8de96a491716ad75d13605a4e` です。
表示された build prefix は、完全な upstream revision の証明ではありません。
互換 version 範囲は推測しません。

実 help は、新しい UUID の明示と resume/fork/continue/code 復元を区別し、通常の tool・permission・subagent・web option を説明します。
root help に no-memory/no-leader は表示されませんが、help 限定の解析では両 option を受け付け、故意の不正 option は失敗しました。
これは parser の受け付けであり、実際の privacy、単独 ownership、tool 安全性の証明ではありません。
source だけの調査では、leader/memory/設定/認証の管理や model command は実行していません。
別の起動試行は下記に記録します。

## model prompt なしの起動試行

空の packet-only fixture cwd から、no-memory/no-subagents/no-leader、空の builtin tools、web 無効、plan mode を明示して通常 TUI の起動を試行しました。
model prompt、承認・信頼・ログイン回答は送らず、source/private workspace は渡さず、raw terminal も保存していません。
元の SessionStart 記録は観測できませんでした。
実所有 process だけを停止して0で Wait しましたが、これは後始末であり model の成功ではありません。
backend の停止や capture 欠落の原因も証明しません。
実効 memory/tool/backend ownership は未認定のままです。
別の read-only inspect は、既知 section の有無・件数だけを保持し、raw config、path、server 名、認証値は保存・共有していません。

## 検証が必要な契約

現在の[公式 hook 文書](https://docs.x.ai/build/features/hooks) は、camelCase の identity/target/tool field と環境 hint を説明します。
多くの hook 失敗が tool 実行を止めないことも記載されています。
失敗する observer を安全 gate と扱わないための注意であり、installed version の native 認定ではありません。
project hook の信頼確認は迂回しません。

Grok は制限付き engine として扱います。
限定した合成・非機密入力だけを使い、private history や credential を収集せず、API/engine を暗黙 fallback しません。
native tool/data 境界を検証するまでは source workspace を読ませません。
option の受け付けだけで memory 除外、実 model/provider、callback 相関、TUI が AI backend を所有することを認定しません。
別の native headless/stdio/leader mode で通常 CLI の契約を黙って置き換えません。

製品コード前に、実 native inventory と独立した Structure-Behavior checkpoint が必要です。
元の主要 capture/stdout/receipt、正確な target/conversation/model/provider、旧管理 owner の Wait、一件だけの明示 managed resume、保留 input/tool/approval の再送防止を検証します。
未対応 live 状態は理由を確認し、未実施テストは未実施とします。
続いて影響・逆依存テスト、最終独立 review、fresh CI/security、同 SHA wave を実施します。
UI、配布、wrapper/broker、本番操作は含めません。
Muse と実機 sleep/wake は別途、保留を維持します。

## 追加の local 専用診断：認定ではない

実際の controlling PTY、同じ固定 executable・制限 flag、新しい空の packet 専用 cwd で診断しました。
文書化された local `/context` を入力しましたが、固有の command completion を確認できなかったため、**Enter は送信せず**、model prompt も要求していません。
結果は FAIL であり、effective memory/tool/backend の証明ではありません。
terminal・account・context 本文は保存していません。

通常の SIGTERM cleanup 後、観測 window 内に実 Wait が得られませんでした。
元 controller は保持し、再起動も終了扱いもしていません。
対象限定の `pgrep` では child が省かれましたが、Apple の exact-parent `proc_listchildpids` で唯一の直接 Grok child と、一致する UID・process group を確認しました。
現在の argv/session metadata と cwd は取得できませんでした。
maintainer は **今回の診断 child 一件だけ** この2つの現在照合を免除し、固定 driver/native source と現在の直接親子関係・UID・group・Grok 名を再確認して SIGKILL を1回送りました。
**signal の送信は実終了ではなく、元の Wait は未完了です。**
group、他の AI、process adoption、製品の所有・終了契約は変更していません。
この owner を照合できるまでは、次の native 試行を開始しません。

診断 harness にも制御経路の欠陥があります。
観測失敗後に PTY の読取りを止め、Popen handle を保持する一方、cleanup command を渡す経路がありません。
製品 daemon の drain/control/Wait は別実装であり、同じ欠陥があるという証拠ではありません。
次回前に、cleanup 中も PTY drain を維持し、実所有 handle への明示制御と Wait の即時保存を用意します。
広域走査や自動 force escalation は行いません。
読取り停止が今回の原因かどうかは未証明です。

## 公式 source との対応は未検証

[公式 CLI reference](https://docs.x.ai/build/cli/reference) は session 限定の制限を説明し、[release notes](https://x.ai/changelog/build) は2026-10-16に1.0.62を掲載しています。
[公式公開 source](https://github.com/xai-org/grok-build) を、そのまま導入済み source と扱ってはいけません。
報告された1474f9cbdd91 prefix の照会は HTTP422で、最初の100 tags に1.0.62一致はなく、現在 SOURCE_REV は異なり、列挙された最新変更日は2026-09-29でした。
この範囲の照会だけで、一致 source が存在しないとは断定しません。
公開 snapshot を導入済み binary と認定せず、binary/source の置換や model/privacy/backend guard の緩和も行いません。

## 公式release artifactとのbyte一致

別のread-only検証で、[公式installer](https://x.ai/cli/install.sh) のURL・platform・圧縮形式の規則を読みました。
installer自体は実行していません。
固定した [1.0.62 macOS ARM64 gzip artifact](https://x.ai/cli/grok-1.0.62-macos-aarch64.gz) を、認証なし・download上限80MiBで取得しました。
圧縮71036654 byteのSHA256は `7a8ada02efca32c4fb065592db5739e497bf87937310f54297845d63e27773cb` です。
上限256MiBのstream展開で175577200 byteを得て、SHA256 `ec434e831afdcfce87780f2815fd732b1df8a4a8de96a491716ad75d13605a4e` が、改めてread-onlyで計算した導入済みexecutableと完全一致しました。

これは公式release artifactとのbyte一致であり、**公開sourceの完全なrevision対応、hook/model/provider/tool/backendや安全な復旧の認定ではありません。**
installer・取得binary・導入済みbinaryを実行せず、install・置換・実行権限の付与、credentialの読み取り、既存processへのsignal送信は行っていません。
元の診断Waitは未完了であり、次のnative試行は開始しません。
先行するsource照会とnative検証の失敗も保持します。
