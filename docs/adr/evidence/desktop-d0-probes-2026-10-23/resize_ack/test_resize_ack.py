import unittest
from resize_ack import Capture, Ledger

class ResizeAcknowledgementTests(unittest.TestCase):
    def setUp(self):
        self.l = Ledger("generation-a", "window-a", "cohort-a")
    def issue(self, i=1, issued=10, deadline=100, width=800, height=600):
        self.l.issue(i, width, height, issued, deadline)
    def capture(self, i=1, **kw):
        d=dict(request_id=i,generation="generation-a",window="window-a",cohort="cohort-a",width=800,height=600,applied_ns=19+i,captured_ns=20+i,capture_id=f"ui-capture-{i}")
        d.update(kw);return Capture(**d)
    def test_immediate_read_is_not_completion(self):
        self.issue();self.l.observe_read(1,800,600,11);self.assertEqual(self.l.outcome(1),"pending")
    def test_same_capture_completion_matches(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(),22),"matched");self.assertEqual(self.l.outcome(1),"matched")
    def test_wrong_identity_does_not_poison_expected_request(self):
        self.issue()
        for kw in [dict(generation="old"),dict(window="foreign"),dict(cohort="rebuilt")]:
            self.assertEqual(self.l.complete(self.capture(**kw),22),"identity_rejected")
        self.assertEqual(self.l.outcome(1),"pending")
    def test_unknown_request_not_shifted_to_current(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(2),22),"unknown_request");self.assertEqual(self.l.outcome(1),"pending")
    def test_reordered_receipts_correlate_by_id(self):
        self.issue(1);self.issue(2,issued=15);self.l.complete(self.capture(2),22);self.l.complete(self.capture(1),23)
        self.assertEqual([self.l.outcome(i) for i in [1,2]],["matched","matched"]);self.assertEqual(self.l.out_of_order,1)
    def test_identical_duplicate_is_idempotent(self):
        self.issue();c=self.capture();self.l.complete(c,22);self.assertEqual(self.l.complete(c,23),"duplicate");self.assertEqual(self.l.counts()["matched"],1)
    def test_conflicting_duplicate_invalidates_success(self):
        self.issue();self.l.complete(self.capture(),22);self.assertEqual(self.l.complete(self.capture(width=900),23),"conflict");self.assertEqual(self.l.outcome(1),"conflict")
    def test_late_receipt_cannot_turn_timeout_into_pass(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(),101),"timed_out");self.assertEqual(self.l.complete(self.capture(),102),"timed_out")
    def test_missing_receipt_expires_without_synthetic_ack(self):
        self.issue();self.l.expire(100);self.assertEqual(self.l.outcome(1),"pending");self.l.expire(101);self.assertEqual(self.l.outcome(1),"timed_out")
    def test_timeout_remains_terminal_even_if_clock_is_replayed(self):
        self.issue();self.l.expire(101);self.assertEqual(self.l.complete(self.capture(),22),"clock_rejected");self.assertEqual(self.l.outcome(1),"timed_out")
    def test_capture_timestamp_chain_must_be_monotonic(self):
        for kw in [dict(applied_ns=9),dict(captured_ns=19),dict(captured_ns=23)]:
            self.l=Ledger("generation-a","window-a","cohort-a");self.issue();self.assertEqual(self.l.complete(self.capture(**kw),22),"capture_rejected");self.assertEqual(self.l.outcome(1),"pending")
    def test_dimension_mismatch_is_not_relabelled_pass(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(height=601),22),"mismatched");self.assertEqual(self.l.outcome(1),"mismatched")
    def test_deadline_boundary_is_explicit(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(),100),"matched")
    def test_200_requests_can_overlap_at_original_cadence(self):
        cadence=150_000_000
        for i in range(1,201):self.l.issue(i,800,600,i*cadence,i*cadence+40_000_000_000)
        self.assertEqual(self.l.counts()["pending"],200)
        for i in range(1,201):self.l.complete(self.capture(i,applied_ns=31_000_000_000+i*3,captured_ns=31_000_000_001+i*3),31_000_000_002+i*3)
        self.assertEqual(self.l.counts()["matched"],200)
    def test_request_count_and_identity_bounds(self):
        self.issue()
        with self.assertRaises(ValueError):self.issue()
        for i in [0,201,True]:
            with self.assertRaises(ValueError):self.issue(i)
    def test_invalid_request_dimensions_and_times(self):
        for kw in [dict(width=0),dict(height=True),dict(issued=-1),dict(deadline=10)]:
            with self.assertRaises(ValueError):self.issue(**kw)
    def test_conflict_cannot_be_resurrected_by_original_receipt(self):
        self.issue();c=self.capture();self.l.complete(c,22);self.l.complete(self.capture(width=900),23);self.l.complete(c,24);self.assertEqual(self.l.outcome(1),"conflict")

    def test_all_three_request_receipt_permutations_preserve_outcomes(self):
        from itertools import permutations
        for order in permutations((1,2,3)):
            ledger=Ledger("generation-a","window-a","cohort-a")
            for i in (1,2,3):ledger.issue(i,800,600,i,100)
            for time,i in enumerate(order,30):ledger.complete(self.capture(i,height=601 if i==2 else 600),time)
            self.assertEqual([ledger.outcome(i) for i in (1,2,3)],["matched","mismatched","matched"])
            expected = sum(i < max(order[:j]) for j,i in enumerate(order) if j)
            self.assertEqual(ledger.out_of_order,expected)

    def test_bool_or_negative_capture_times_are_rejected(self):
        for bad in (True,-1):
            self.l=Ledger("generation-a","window-a","cohort-a");self.issue()
            self.assertEqual(self.l.complete(self.capture(applied_ns=bad),22),"capture_rejected")
            self.assertEqual(self.l.outcome(1),"pending")

    def test_wrong_cohort_cannot_create_conflict_after_valid_capture(self):
        self.issue();self.l.complete(self.capture(),22)
        self.assertEqual(self.l.complete(self.capture(cohort="another-build",width=900),23),"identity_rejected")
        self.assertEqual(self.l.outcome(1),"matched")

    def test_many_invalid_requests_do_not_grow_storage(self):
        self.issue()
        for i in range(201,1001):self.assertEqual(self.l.complete(self.capture(i),22),"unknown_request")
        self.assertEqual(len(self.l.requests),1)
        self.assertEqual(len(self.l.diagnostics),1)

    def test_late_duplicate_of_already_timely_capture_does_not_add_success(self):
        self.issue();c=self.capture();self.l.complete(c,22)
        self.assertEqual(self.l.complete(c,200),"duplicate")
        self.assertEqual(self.l.counts()["matched"],1)

    def test_reused_capture_time_pair_cannot_count_as_two_resizes(self):
        self.issue(1);self.issue(2,issued=15)
        self.l.complete(self.capture(1),22)
        self.assertEqual(self.l.complete(self.capture(2,applied_ns=20,captured_ns=21),23),"capture_reused")
        self.assertEqual([self.l.outcome(i) for i in (1,2)],["conflict","conflict"])

    def test_reused_capture_id_with_changed_times_is_ambiguous(self):
        self.issue(1);self.issue(2,issued=15)
        self.l.complete(self.capture(1,capture_id="same-ui-capture"),22)
        self.assertEqual(self.l.complete(self.capture(2,capture_id="same-ui-capture",applied_ns=23,captured_ns=24),25),"capture_reused")
        self.assertEqual(self.l.counts()["matched"],0)

    def test_delayed_capture_after_next_issue_uses_current_receipt_clock(self):
        self.issue(1,issued=150_000_000,deadline=650_000_000)
        self.issue(2,issued=300_000_000,deadline=800_000_000)
        c=self.capture(1,applied_ns=200_000_000,captured_ns=201_000_000)
        self.assertEqual(self.l.complete(c,375_000_000),"matched")

    def test_old_ingress_clock_is_not_current_runner_receipt_clock(self):
        self.issue(1,issued=150_000_000,deadline=650_000_000)
        self.issue(2,issued=300_000_000,deadline=800_000_000)
        c=self.capture(1,applied_ns=200_000_000,captured_ns=201_000_000)
        self.assertEqual(self.l.complete(c,250_000_000),"clock_rejected")

    def test_scope_is_immutable_and_recreation_needs_new_ledger(self):
        for name in ("generation","window","cohort"):
            with self.assertRaises(AttributeError):setattr(self.l,name,"new")

    def test_diagnostic_read_snapshot_does_not_complete_request(self):
        self.issue();self.l.observe_read(1,800,600,11)
        self.assertEqual(self.l.diagnostic_read(1),(800,600,11))
        self.assertEqual(self.l.counts()["matched"],0)

    def test_final_expiry_counts_requests_not_duplicate_event_diagnostics(self):
        self.issue();self.l.expire(101)
        self.assertEqual(self.l.counts()["timed_out"],1)
        for time in (102,103):self.l.complete(self.capture(),time)
        self.assertEqual(self.l.counts()["timed_out"],1)
        self.assertEqual(self.l.diagnostics["timed_out"],2)

    def test_200_interleaved_requests_keep_150ms_cadence_and_explicit_500ms_fixture_deadlines(self):
        cadence=150_000_000
        for i in range(1,201):
            self.l.issue(i,800+i%2,600,i*cadence,i*cadence+500_000_000)
            if i>1:
                prior=i-1;t=prior*cadence+75_000_000
                c=self.capture(prior,width=800+prior%2,applied_ns=t,captured_ns=t+1)
                self.assertEqual(self.l.complete(c,i*cadence+25_000_000),"matched")
        t=200*cadence+75_000_000
        self.l.complete(self.capture(200,width=800,applied_ns=t,captured_ns=t+1),200*cadence+175_000_000)
        self.l.expire(200*cadence+500_000_001)
        self.assertEqual(self.l.counts()["matched"],200)
        self.assertEqual(len(self.l.capture_ids),200)
        self.assertEqual(len(self.l.capture_pairs),200)

    def test_missing_capture_id_cannot_match(self):
        self.issue();self.assertEqual(self.l.complete(self.capture(capture_id=""),22),"capture_rejected")
        self.assertEqual(self.l.outcome(1),"pending")

if __name__ == "__main__":unittest.main()
