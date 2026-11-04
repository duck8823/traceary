# Grok Build native 認定: 1.0.62

[English](grok-1.0.62.md)

**状態：調査のみ。native AI turn、callback、安全な復旧は未認定です。** [#2477](https://github.com/duck8823/traceary/issues/2477) を追跡し、親を完了扱いにしません。
[本文を含まない metadata](evidence/grok-1.0.62.json) で実測と未実施 gate を区別します。

## 実測 source

導入済みの直接実行する arm64 binary は `grok 1.0.62 (1474f9cbdd91)` と報告し、SHA256 は `ec434e831afdcfce87780f2815fd732b1df8a4a8de96a491716ad75d13605a4e` です。
表示された build prefix は、完全な upstream revision の証明ではありません。
互換 version 範囲は推測しません。

実 help は、新しい UUID の明示と resume/fork/continue/code 復元を区別し、通常の tool・permission・subagent・web option を説明します。
root help に no-memory/no-leader は表示されませんが、help 限定の解析では両 option を受け付け、故意の不正 option は失敗しました。
これは parser の受け付けであり、実際の privacy、単独 ownership、tool 安全性の証明ではありません。
leader、memory、設定、認証、model の操作は行っていません。

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
