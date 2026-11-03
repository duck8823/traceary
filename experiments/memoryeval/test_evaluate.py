import copy
import json
from pathlib import Path
import unittest

import evaluate


class EvaluationTest(unittest.TestCase):
    def setUp(self):
        self.corpus = json.loads(Path(evaluate.__file__).with_name("corpus.json").read_text())
        self.inputs = {"corpus_sha256": evaluate.digest(json.dumps(self.corpus, sort_keys=True)), "rows": []}
        for task in self.corpus["tasks"]:
            for arm in evaluate.ARMS:
                context = "Evidence ID: synthetic-event\nsynthetic evidence"
                body = evaluate.prompt(task, context)
                self.inputs["rows"].append({"task": task["id"], "arm": arm, "prompt": body,
                    "prompt_sha256": evaluate.digest(body), "coverage": arm != "no-history", "allowed_evidence": ["synthetic-event"],
                    "context_bytes": len(context.encode()), "context_budget_bytes": self.corpus["context_budget_bytes"],
                    "output_budget_words": self.corpus["output_budget_words"]})

    def receipt(self, index=0):
        row = self.inputs["rows"][index]
        task = next(task for task in self.corpus["tasks"] if task["id"] == row["task"])
        return {"task": row["task"], "arm": row["arm"], "prompt_sha256": row["prompt_sha256"],
                "host": "synthetic-host-v1", "model": "synthetic-model-v1", "response": {"answer": task["answer"], "evidence": ["synthetic-event"]},
                "input_tokens": 10, "output_tokens": 5, "latency_ms": 1.0, "api_cost_usd": None}

    def test_missing_responses_are_unmeasured_not_losses(self):
        report = evaluate.score(self.corpus, self.inputs, [])
        self.assertEqual(report["measured_responses"], 0)
        self.assertEqual(len(report["unmeasured"]), 15)
        self.assertEqual(report["paired_tasks"], [])

    def test_paired_outcomes_require_all_arms(self):
        receipts = [self.receipt(index) for index in range(3)]
        receipts[0]["response"]["answer"] = "UNKNOWN"
        report = evaluate.score(self.corpus, self.inputs, receipts)
        self.assertEqual(len(report["paired_tasks"]), 1)
        self.assertFalse(report["paired_tasks"][0]["outcomes"]["no-history"])
        self.assertTrue(report["paired_tasks"][0]["outcomes"]["staged"])

    def test_forged_or_foreign_evidence_is_not_supported(self):
        receipt = self.receipt()
        receipt["response"]["evidence"] = ["foreign-workspace-event"]
        result = evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]
        self.assertFalse(result["correct"])
        self.assertTrue(result["unsupported_evidence"])

    def test_wrong_answer_is_a_measured_failure(self):
        receipt = self.receipt()
        receipt["response"]["answer"] = "SMALLER_BINARY"
        self.assertFalse(evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]["correct"])

    def test_prompt_tampering_fails(self):
        receipt = self.receipt()
        self.inputs["rows"][0]["prompt"] += "changed"
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [receipt])

    def test_corpus_tampering_fails(self):
        self.corpus["version"] = 2
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])

    def test_duplicate_response_and_missing_input_fail(self):
        receipt = self.receipt()
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [receipt, receipt])
        self.inputs["rows"].pop()
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])

    def test_mixed_models_fail(self):
        a, b = self.receipt(), self.receipt(1)
        b["model"] = "another-model"
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [a, b])

    def test_invalid_usage_fails_but_unknown_is_not_zero(self):
        for value in (-1, True, float("nan"), "100"):
            receipt = self.receipt()
            receipt["input_tokens"] = value
            with self.assertRaises(ValueError):
                evaluate.score(self.corpus, self.inputs, [receipt])
        receipt = self.receipt()
        receipt["input_tokens"] = None
        self.assertIsNone(evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]["usage"]["input_tokens"])

    def test_prompt_does_not_serialize_answer_key_or_support(self):
        task = copy.deepcopy(self.corpus["tasks"][0])
        task["answer"] = "HIDDEN_EXPECTED_VALUE"
        task["support"] = ["HIDDEN_RUBRIC"]
        body = evaluate.prompt(task, "evidence")
        self.assertNotIn("HIDDEN_EXPECTED_VALUE", body)
        self.assertNotIn("HIDDEN_RUBRIC", body)

    def test_modified_budget_or_undelivered_evidence_rejected(self):
        self.inputs["rows"][0]["context_budget_bytes"] = 9999
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])
        self.setUp()
        self.inputs["rows"][0]["allowed_evidence"] = ["not-delivered"]
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])


if __name__ == "__main__":
    unittest.main()
