# Security policy

[English](./SECURITY.md)

Traceary に脆弱性があると思われる場合は、まず public issue を立てずに連絡してください。

## 推奨する報告経路

- maintainer へのメール: `duck8823@gmail.com`
- この repository で GitHub の private vulnerability reporting が有効になっている場合は、その private report flow でも構いません

## 含めてほしい情報

可能な範囲で、次の情報を含めてください。

- 影響を受ける Traceary の version / tag / commit SHA
- 再現手順または最小の proof of concept
- 想定される impact
- 機微データの露出や永続化が発生したか

## 返信目安

Traceary は best-effort で保守しています。目標としては 7 日以内に受領を返し、その後できるだけ早く fix または mitigation 方針を調整します。

## サポート対象バージョン

security fix は best-effort で次を対象にします。

- 最新の tag 付き release
- 現在の `main` branch
