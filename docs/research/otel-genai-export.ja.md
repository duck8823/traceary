# 決定: OTel GenAI export (#1258)

[English](./otel-genai-export.md)

**Status:** v0.26.0 では no-go（文書化された決定）  
**Date:** 2026-07-16  
**Issue:** #1258

## 決定

v0.26.0 ではネットワーク経由の OTel GenAI export を**出荷しない**。Traceary は local-first を維持し、GenAI semantic conventions が安定し、具体的な consumer 要件が現れるまで export を既定 product surface に載せない。

## 一次情報

- OpenTelemetry GenAI semantic conventions: https://opentelemetry.io/docs/specs/semconv/gen-ai/
- GenAI conventions リポジトリ: https://github.com/open-telemetry/semantic-conventions-genai
- OTel blog (2026-05): https://opentelemetry.io/blog/2026/genai-observability/

## 調査結果

- 2026 年中盤時点でも GenAI semantic conventions は **development / experimental**。属性名・形状は dual-emission / opt-in が必要な状況が続く。
- Traceary が session / audit / transcript を固定マッピングできる「凍結済み」属性集合はまだない。
- sensitive-path / redaction / coverage / host-reported model はローカル監査 claim であり、既定でネットワーク送信すると local-first 方針と衝突する。

## 再検討条件

- GenAI conventions が stable になる（または pin 可能な凍結 subset）。
- 明示的な consumer があり experimental 属性の変化を許容する。
- export が **opt-in**（既定オフ）で、private payload を span に載せない設計。

## 確認した Non-goals

- 既定のネットワーク telemetry。
- 評価 PR 内での export 実装。
