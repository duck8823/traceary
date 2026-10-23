"""Synthetic receipt classifier. It proves no native UI or security property."""
from collections import Counter
from dataclasses import dataclass


def _integer(value, minimum=0):
    return type(value) is int and value >= minimum


@dataclass(frozen=True)
class Capture:
    request_id: int
    generation: str
    window: str
    cohort: str
    width: int
    height: int
    applied_ns: int
    captured_ns: int
    capture_id: str


@dataclass
class Request:
    width: int
    height: int
    issued_ns: int
    deadline_ns: int
    state: str = "pending"
    capture: Capture | None = None
    provisional_read: tuple | None = None


class Ledger:
    """Owned by one trusted measurement runner, never a renderer RPC endpoint.

    Completion ingress and clock provenance require separate native validation.
    Identity is experiment correlation only, not authentication or input authority.
    """
    def __init__(self, generation, window, cohort, max_requests=200):
        for value in (generation, window, cohort):
            if not isinstance(value, str) or not 1 <= len(value) <= 128:
                raise ValueError("bounded correlation identity required")
        if not _integer(max_requests, 1) or max_requests > 200:
            raise ValueError("at most 200 experiment requests")
        self._scope = (generation, window, cohort)
        self.max_requests = max_requests
        self.requests = {}
        self.last_clock = 0
        self.highest_completed_id = 0
        self.out_of_order = 0
        self.diagnostics = Counter()
        self.capture_ids = {}
        self.capture_pairs = {}

    @property
    def generation(self):
        return self._scope[0]

    @property
    def window(self):
        return self._scope[1]

    @property
    def cohort(self):
        return self._scope[2]

    def diagnostic_read(self, request_id):
        return self.requests[request_id].provisional_read

    def issue(self, request_id, width, height, issued_ns, deadline_ns):
        if not _integer(request_id, 1) or request_id > self.max_requests:
            raise ValueError("bounded request identity required")
        if request_id in self.requests or len(self.requests) >= self.max_requests:
            raise ValueError("duplicate or excess request")
        if not all(_integer(x, 1) for x in (width, height)):
            raise ValueError("positive integer dimensions required")
        if not _integer(issued_ns) or not _integer(deadline_ns) or deadline_ns <= issued_ns:
            raise ValueError("explicit future monotonic deadline required")
        if issued_ns < self.last_clock:
            raise ValueError("measurement clock reversed")
        self.requests[request_id] = Request(width, height, issued_ns, deadline_ns)
        self.last_clock = issued_ns

    def outcome(self, request_id):
        return self.requests[request_id].state

    def counts(self):
        result = dict.fromkeys(("pending", "matched", "mismatched", "timed_out", "conflict"), 0)
        result.update(Counter(r.state for r in self.requests.values()))
        return result

    def _clock(self, now):
        if not _integer(now) or now < self.last_clock:
            return False
        self.last_clock = now
        return True

    def _result(self, name):
        self.diagnostics[name] += 1
        return name

    def observe_read(self, request_id, width, height, now):
        """An immediate getter result is diagnostic, never a completion receipt."""
        if not self._clock(now):
            return self._result("clock_rejected")
        request = self.requests.get(request_id)
        if request is None:
            return self._result("unknown_request")
        if not all(_integer(x, 1) for x in (width, height)):
            return self._result("capture_rejected")
        request.provisional_read = (width, height, now)
        return self._result("read_only")

    def complete(self, capture, received_ns):
        if not isinstance(capture, Capture) or not _integer(capture.request_id, 1):
            return self._result("capture_rejected")
        request = self.requests.get(capture.request_id)
        if request is None:
            return self._result("unknown_request")
        if (capture.generation, capture.window, capture.cohort) != (self.generation, self.window, self.cohort):
            return self._result("identity_rejected")
        if not self._clock(received_ns):
            return self._result("clock_rejected")
        if request.state in ("timed_out", "conflict"):
            return self._result(request.state)
        if request.state == "pending" and received_ns > request.deadline_ns:
            request.state = "timed_out"
            return self._result("timed_out")
        if not all(_integer(x, 1) for x in (capture.width, capture.height)):
            return self._result("capture_rejected")
        if not all(_integer(x) for x in (capture.applied_ns, capture.captured_ns)):
            return self._result("capture_rejected")
        if not isinstance(capture.capture_id, str) or not 1 <= len(capture.capture_id) <= 128:
            return self._result("capture_rejected")
        if not request.issued_ns <= capture.applied_ns <= capture.captured_ns <= received_ns:
            return self._result("capture_rejected")
        if request.capture is not None:
            if request.capture == capture:
                return self._result("duplicate")
            request.state = "conflict"
            return self._result("conflict")
        pair = (capture.applied_ns, capture.captured_ns)
        owners = {self.capture_ids.get(capture.capture_id), self.capture_pairs.get(pair)} - {None}
        if owners - {capture.request_id}:
            request.state = "conflict"
            for owner in owners:
                self.requests[owner].state = "conflict"
            return self._result("capture_reused")
        self.capture_ids[capture.capture_id] = capture.request_id
        self.capture_pairs[pair] = capture.request_id
        request.capture = capture
        # Count receipts below the prior high-water ID, not inversion pairs.
        if capture.request_id < self.highest_completed_id:
            self.out_of_order += 1
        self.highest_completed_id = max(self.highest_completed_id, capture.request_id)
        request.state = "matched" if (capture.width, capture.height) == (request.width, request.height) else "mismatched"
        return self._result(request.state)

    def expire(self, now):
        if not self._clock(now):
            return self._result("clock_rejected")
        for request in self.requests.values():
            if request.state == "pending" and now > request.deadline_ns:
                request.state = "timed_out"
        return self._result("expiry_checked")
