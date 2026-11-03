# host 側の教訓レビュー用テンプレート

[English](lesson-review-template.md)

candidate の下書き・レビューにだけ使います。保存・採用・有効化・実行・install の指示ではありません。過去の承認や資格情報を転用可能な教訓として扱いません。

## 候補の確認票

- 候補 ID・版と、提案する fact の正確な内容:
- scope と validity:
- 適用条件:
- 失敗した試行の参照と、独立に確認できる結果:
- 成功した試行の参照と、独立に確認できる結果:
- 観測したことと推論したことの区別:
- 既知の失敗条件と反例 task:
- 除去する task 固有の値・path、棄却する古い助言:
- 矛盾・失効した証拠:
- held-out の task family・時刻分割と、固定した候補 hash:
- 同条件の no-lesson/current-summary/lesson 試験結果:
- token・遅延・生成/レビュー負荷・価格。不明をゼロにしない:
- 誤適用・反復した失敗・不確実性・未試験:
- 判断: reject / defer / 明示的な操作者レビューへの提案:
- rollback・retire の条件:

## 既存 Traceary field への対応

限定した最終規則を type=lesson の fact に置き、既存の workspace/agent/session-family scope を使います。両試行を evidence ref、確認票・結果確認を artifact ref にします。必要なら既存 validity を明示します。詳細な確認票は artifact に保ち、schema の列や生成 skill を追加しません。

memory store propose は candidate を作ります。操作者が記述する最終 distillation/review は別操作であり、処理後の正確な文・元証拠・scope・validity を確認します。candidate/accepted は tool 権限や実行許可を変更しません。このテンプレートから CLI 実行・自動採用・host activation・skill install・model routing・指示書換えを起動しません。
