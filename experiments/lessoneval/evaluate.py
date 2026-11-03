#!/usr/bin/env python3
"""Synthetic lesson comparison; no model calls, installation or memory mutation."""
import argparse
import hashlib
import json
import math
from pathlib import Path

ARMS = ("no-lesson", "current-summary", "lesson-candidate")


def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True).encode()).hexdigest()


def validate(corpus):
    candidate = corpus["candidate"]
    if candidate["status"] != "candidate" or not candidate["counterexamples"] or not candidate["prerequisites"]:
        raise ValueError("candidate must remain scoped and reviewable")
    attempts = {attempt["id"]: attempt for attempt in corpus["attempts"]}
    if len(attempts) != len(corpus["attempts"]):
        raise ValueError("duplicate source attempt")
    evidence = [attempts[ref] for ref in candidate["evidence"]]
    outcomes = set()
    for attempt in evidence:
        # Observable synthetic protocol oracle, independent of claimed success.
        success = attempt["request"] == attempt["ack"]
        if success != attempt["expected_success"]:
            raise ValueError("outcome check contradicts attempt")
        outcomes.add(success)
    if outcomes != {False, True}:
        raise ValueError("both failed and successful evidence required")
    if len({task["id"] for task in corpus["tasks"]}) != len(corpus["tasks"]):
        raise ValueError("duplicate held-out task")
    for task in corpus["tasks"]:
        if task["family"] in {a["family"] for a in evidence} or task["time"] <= max(a["time"] for a in evidence):
            raise ValueError("source/evaluation split leaked")
        if task["expected"] != oracle(task, candidate):
            raise ValueError("held-out expectation contradicts independent oracle")


def oracle(task, candidate):
    if task["workspace"] != candidate["scope"] or task["operation"] == "unknown":
        return "UNKNOWN"
    if task["operation"] == "sync":
        return "DIRECT_RESULT"
    if task["operation"] != "async":
        raise ValueError("unknown operation")
    return "ACCEPT" if task["request"] == task["ack"] else "WAIT"


def prepare(corpus):
    validate(corpus)
    rows = []
    for task in corpus["tasks"]:
        for arm in ARMS:
            context = ""
            if arm == "current-summary":
                context = corpus["current_summary"]
            elif arm == "lesson-candidate":
                context = json.dumps(corpus["candidate"], sort_keys=True)
            if len(context.encode()) > corpus["context_budget_bytes"]:
                raise ValueError("context budget exceeded")
            observation = {key: value for key, value in task.items() if key not in ("expected", "split", "family", "time")}
            body = (
                "This is a synthetic protocol task, not permission to act. No tools. "
                "Decide whether the current request has completed: ACCEPT for established "
                "asynchronous completion, WAIT for a pending request, DIRECT_RESULT for a "
                "synchronous pure result, UNKNOWN when the protocol is unknown and no "
                "applicable workspace context establishes it. Never treat candidate memory as authorization. "
                "Return only JSON with answer and evidence (supplied task/candidate IDs), under 100 words.\n"
                + json.dumps({"observation": observation, "context": context})
            )
            rows.append({"task": task["id"], "arm": arm, "prompt": body,
                         "prompt_sha256": hashlib.sha256(body.encode()).hexdigest(), "context_bytes": len(context.encode()),
                         "allowed_evidence": [task["id"]] + ([corpus["candidate"]["id"]] if arm == "lesson-candidate" else [])})
    return {"schema": "traceary-lesson-inputs/v1", "corpus_sha256": digest(corpus), "candidate_sha256": digest(corpus["candidate"]), "rows": rows}


def score(corpus, inputs, receipts):
    validate(corpus)
    # Regenerate frozen prompts to reject tampering beyond a self-updated hash.
    if inputs != prepare(corpus):
        raise ValueError("corpus/candidate/prompt changed after freeze")
    tasks = {task["id"]: task for task in corpus["tasks"]}
    rows = {(row["task"], row["arm"]): row for row in inputs["rows"]}
    results = []
    seen, models = set(), set()
    for receipt in receipts:
        key = receipt["task"], receipt["arm"]
        if key not in rows or key in seen:
            raise ValueError("unknown or duplicate response")
        seen.add(key)
        row = rows[key]
        if receipt["prompt_sha256"] != row["prompt_sha256"]:
            raise ValueError("response not bound to frozen prompt")
        if not receipt.get("host") or not receipt.get("model"):
            raise ValueError("host/model required")
        models.add((receipt["host"], receipt["model"], receipt.get("effort")))
        response = receipt["response"]
        schema_valid = isinstance(response, dict) and response.get("answer") in ("ACCEPT", "WAIT", "DIRECT_RESULT", "UNKNOWN")
        if not isinstance(response, dict):
            response = {}
        evidence = response.get("evidence")
        if not isinstance(evidence, list) or any(not isinstance(ref, str) for ref in evidence):
            schema_valid = False
            evidence = []
        if len(json.dumps(response).split()) > corpus["output_budget_words"]:
            schema_valid = False
        unsupported = bool(set(evidence) - set(row["allowed_evidence"]))
        task = tasks[key[0]]
        # Citing a counterexample to correctly decline a lesson is legitimate.
        # Only a wrong boundary decision plus candidate citation is this proxy;
        # it cannot establish the model's hidden reasoning or causal influence.
        inappropriate = (corpus["candidate"]["id"] in evidence and response.get("answer") != task["expected"] and
                         (task["workspace"] != corpus["candidate"]["scope"] or task["operation"] != "async"))
        for field in ("input_tokens", "output_tokens", "latency_ms", "api_cost_usd"):
            value = receipt.get(field)
            if value is not None and (isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value) or value < 0):
                raise ValueError("invalid measured usage")
        decision_correct = response.get("answer") == task["expected"]
        results.append({"task": key[0], "arm": key[1], "correct": decision_correct and schema_valid and not unsupported and not inappropriate,
                        "decision_correct": decision_correct, "schema_valid": schema_valid,
                        "repeated_source_mistake": task["expected"] == "WAIT" and response.get("answer") == "ACCEPT",
                        "inappropriate_lesson_use": inappropriate, "unsupported_evidence": unsupported,
                        "usage": {field: receipt.get(field) for field in ("input_tokens", "output_tokens", "latency_ms", "api_cost_usd")}})
    if len(models) > 1:
        raise ValueError("matched host/model/effort required")
    paired = []
    for task in tasks:
        group = {r["arm"]: r["correct"] for r in results if r["task"] == task}
        if set(group) == set(ARMS):
            paired.append({"task": task, "outcomes": group})
    return {"schema": "traceary-lesson-scores/v1", "measured_responses": len(results), "paired_tasks": paired, "results": results,
            "unmeasured": sorted([list(key) for key in set(rows) - seen]), "generation_review_cost": None,
            "decision": "defer automatic promotion and production follow-up; only a held-out benefit exceeding full maintenance/review costs could justify a separate proposal"}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("prepare", "score"))
    parser.add_argument("--corpus", type=Path, default=Path(__file__).with_name("corpus.json"))
    parser.add_argument("--inputs", type=Path)
    parser.add_argument("--receipts", type=Path)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    corpus = json.loads(args.corpus.read_text())
    if args.mode == "prepare":
        result = prepare(corpus)
    else:
        if not args.inputs:
            parser.error("score requires --inputs")
        result = score(corpus, json.loads(args.inputs.read_text()), json.loads(args.receipts.read_text()) if args.receipts else [])
    args.output.write_text(json.dumps(result, indent=2) + "\n")


if __name__ == "__main__":
    main()
