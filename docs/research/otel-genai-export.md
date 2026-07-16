# Decision: OTel GenAI export (#1258)

[日本語](./otel-genai-export.ja.md)

**Status:** no-go for v0.26.0 (documented decision)  
**Date:** 2026-07-16  
**Issue:** #1258

## Decision

Do **not** ship network OTel GenAI export in v0.26.0. Traceary remains local-first; export stays out of the default product surface until GenAI semantic conventions stabilize and a concrete consumer requirement exists.

## Primary sources

- OpenTelemetry GenAI semantic conventions home (moved / active development): https://opentelemetry.io/docs/specs/semconv/gen-ai/
- GenAI conventions repository: https://github.com/open-telemetry/semantic-conventions-genai
- OTel blog on GenAI observability (2026-05): https://opentelemetry.io/blog/2026/genai-observability/

## Findings

- GenAI semantic conventions remain in **development / experimental** status as of mid-2026. Attribute names and shapes still require stability opt-in dual-emission in several ecosystems.
- There is no single stable, frozen attribute set that Traceary can map sessions / audits / transcripts to without risking breakages on semconv bumps.
- Traceary’s stored claims (sensitive-path, redaction, coverage, host-reported model) are local audit semantics; pushing them over the network by default conflicts with the local-first product stance.

## Conditions to revisit

- GenAI conventions marked stable (or a frozen subset Traceary can pin).
- An explicit consumer (user or integration) that needs OTLP export and accepts experimental attribute churn.
- A design that keeps export **opt-in**, off by default, with no private payloads in spans unless explicitly enabled.

## Non-goals confirmed

- Default network telemetry.
- Shipping export code in the evaluation PR.
