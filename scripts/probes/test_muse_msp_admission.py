"""Offline correlation fixtures; no provider process or model calls."""

import importlib.util
from pathlib import Path
import unittest

SPEC = importlib.util.spec_from_file_location(
    "muse_probe", Path(__file__).with_name("muse-msp-admission.py")
)
PROBE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PROBE)


class AdmissionCorrelationTests(unittest.TestCase):
    def test_reordered_replies_select_actual_queued_request(self):
        replies = [
            ("second", {"commandId": "second", "turnId": "second", "disposition": "started"}),
            ("first", {"commandId": "first", "turnId": "first", "disposition": "queued"}),
        ]
        command, result = PROBE.queued_admission(replies)
        self.assertEqual(command, "first")
        self.assertEqual(result["turnId"], "first")

    def test_distinct_turn_cannot_rebind_request(self):
        result = {"commandId": "first", "turnId": "second", "disposition": "queued"}
        with self.assertRaisesRegex(RuntimeError, "outside this proof"):
            PROBE.queued_admission([("first", result)])
        self.assertEqual(result["commandId"], "first")

    def test_mismatched_echo_is_rejected_even_when_turn_matches(self):
        with self.assertRaisesRegex(RuntimeError, "different command ID"):
            PROBE.correlate_admission({"commandId": "second", "turnId": "first"}, "first")

    def test_structured_rejection_preserved_without_arbitrary_text(self):
        command = "01a0e1a2-d3f1-7559-9394-9b923c7e42e1"
        error = PROBE.RPCError("turn/start", {"code": -32030, "message": "private",
            "data": {"kind": "commandRejected", "reason": "abandoned",
                     "commandId": command, "retryable": False, "details": "private"}})
        self.assertEqual(error.fields, {"code": -32030, "kind": "commandRejected",
                                      "reason": "abandoned", "commandId": command,
                                      "retryable": False})

    def test_unknown_error_strings_and_invalid_identity_are_omitted(self):
        error = PROBE.RPCError("turn/start", {"code": -32030, "message": "private",
            "data": {"kind": "private", "reason": "private", "commandId": "private",
                     "retryable": "false", "details": "private"}})
        self.assertEqual(error.fields, {"code": -32030})


if __name__ == "__main__":
    unittest.main()
