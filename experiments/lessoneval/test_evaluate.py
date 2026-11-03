import copy
import json
from pathlib import Path
import unittest

import evaluate


class LessonEvaluationTest(unittest.TestCase):
    def setUp(self):
        self.corpus = json.loads(Path(evaluate.__file__).with_name("corpus.json").read_text())
        self.inputs = evaluate.prepare(self.corpus)

    def receipt(self, index=0):
        row = self.inputs["rows"][index]
        task = next(t for t in self.corpus["tasks"] if t["id"] == row["task"])
        return {"task": row["task"], "arm": row["arm"], "prompt_sha256": row["prompt_sha256"],
                "host": "synthetic-host-v1", "model": "synthetic-model-v1", "effort": "fixed",
                "response": {"answer": task["expected"], "evidence": [task["id"]]},
                "input_tokens": 10, "output_tokens": 5, "latency_ms": 1.0, "api_cost_usd": None}

    def test_no_receipts_are_unmeasured(self):
        result = evaluate.score(self.corpus, self.inputs, [])
        self.assertEqual(result["measured_responses"], 0)
        self.assertEqual(len(result["unmeasured"]), 15)
        self.assertIsNone(result["generation_review_cost"])

    def test_pair_records_ties_not_manufactured_benefit(self):
        result = evaluate.score(self.corpus, self.inputs, [self.receipt(i) for i in range(3)])
        self.assertEqual(len(result["paired_tasks"]), 1)
        self.assertEqual(set(result["paired_tasks"][0]["outcomes"].values()), {True})

    def test_success_only_lesson_rejected(self):
        self.corpus["candidate"]["evidence"] = ["geometry-success"]
        with self.assertRaises(ValueError):
            evaluate.prepare(self.corpus)

    def test_claimed_success_does_not_override_outcome(self):
        self.assertTrue(self.corpus["attempts"][0]["claimed_success"])
        self.corpus["attempts"][0]["expected_success"] = True
        with self.assertRaises(ValueError):
            evaluate.prepare(self.corpus)

    def test_source_family_or_time_overlap_rejected(self):
        for key, value in (("family", "geometry"), ("time", "2026-01-01")):
            corpus = copy.deepcopy(self.corpus)
            corpus["tasks"][0][key] = value
            with self.assertRaises(ValueError):
                evaluate.prepare(corpus)

    def test_frozen_candidate_and_prompt_must_not_change(self):
        self.inputs["rows"][0]["prompt"] += "changed"
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])
        self.inputs = evaluate.prepare(self.corpus)
        self.corpus["candidate"]["fact"] += "changed"
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [])

    def test_stale_ack_repeats_source_mistake(self):
        receipt = self.receipt(3)
        receipt["response"]["answer"] = "ACCEPT"
        result = evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]
        self.assertTrue(result["repeated_source_mistake"])
        self.assertFalse(result["correct"])

    def test_out_of_scope_and_sync_lesson_citations_fail(self):
        for index in (11, 14):
            receipt = self.receipt(index)
            receipt["response"]["evidence"].append(self.corpus["candidate"]["id"])
            receipt["response"]["answer"] = "WAIT"
            result = evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]
            self.assertTrue(result["inappropriate_lesson_use"])
            self.assertFalse(result["correct"])

    def test_citing_counterexample_to_decline_is_legitimate(self):
        receipt = self.receipt(11)
        receipt["response"]["evidence"].append(self.corpus["candidate"]["id"])
        result = evaluate.score(self.corpus, self.inputs, [receipt])["results"][0]
        self.assertTrue(result["correct"])
        self.assertFalse(result["inappropriate_lesson_use"])

    def test_mixed_model_duplicate_or_bad_usage_rejected(self):
        receipt = self.receipt()
        for field, value in (("input_tokens", -1), ("output_tokens", True), ("api_cost_usd", float("nan"))):
            bad = copy.deepcopy(receipt)
            bad[field] = value
            with self.assertRaises(ValueError):
                evaluate.score(self.corpus, self.inputs, [bad])
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [receipt, receipt])
        other = self.receipt(1)
        other["model"] = "other"
        with self.assertRaises(ValueError):
            evaluate.score(self.corpus, self.inputs, [receipt, other])

    def test_expected_answers_not_in_model_payload(self):
        for row in self.inputs["rows"]:
            self.assertNotIn('"expected":', row["prompt"])
            self.assertNotIn('"expected_success":', row["prompt"])

    def test_malformed_model_response_is_measured_not_unmeasured(self):
        receipt = self.receipt()
        receipt["response"]["evidence"] = {"task_id": receipt["task"]}
        result = evaluate.score(self.corpus, self.inputs, [receipt])
        self.assertEqual(result["measured_responses"], 1)
        self.assertFalse(result["results"][0]["schema_valid"])
        self.assertFalse(result["results"][0]["correct"])
        self.assertTrue(result["results"][0]["decision_correct"])


if __name__ == "__main__":
    unittest.main()
