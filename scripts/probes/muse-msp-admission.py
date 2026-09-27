#!/usr/bin/env python3
"""Probe MSP crash admission in a disposable, credential-free echo session."""

import argparse
import json
import os
from pathlib import Path
import secrets
import select
import shutil
import subprocess
import tempfile
import time
import uuid


class RPCError(RuntimeError):
    """Retain protocol semantics without arbitrary provider message/detail text."""

    def __init__(self, method, error):
        code = error.get("code")
        code = code if isinstance(code, int) else None
        self.fields = {"code": code}
        data = error.get("data")
        if isinstance(data, dict):
            if data.get("kind") in ("commandRejected", "backpressured", "invalidParams",
                                     "sessionNotLoaded", "sessionStreamMismatch"):
                self.fields["kind"] = data["kind"]
            if data.get("reason") in ("abandoned", "missing_run", "session_id_conflict"):
                self.fields["reason"] = data["reason"]
            try:
                self.fields["commandId"] = str(uuid.UUID(data["commandId"]))
            except (KeyError, ValueError, TypeError, AttributeError):
                pass
            if isinstance(data.get("retryable"), bool):
                self.fields["retryable"] = data["retryable"]
        super().__init__(f"MSP {method} error code {code}")


def correlate_admission(result, submitted_command_id):
    """This bounded proof supports direct command-to-turn correlation only."""
    if result.get("commandId") != submitted_command_id:
        raise RuntimeError("MSP admission echoed a different command ID")
    if result.get("turnId") != submitted_command_id:
        raise RuntimeError("MSP distinct turn identity is outside this proof")
    return result


def queued_admission(replies):
    """Select by immutable submitted command ID, never by returned turn ID."""
    queued = []
    for command_id, result in replies:
        correlate_admission(result, command_id)
        if result.get("disposition") == "queued":
            queued.append((command_id, result))
    if len(queued) != 1:
        raise RuntimeError("queued crash window not reached")
    return queued[0]


def uuid7():
    """Generate the session/command UUID version accepted by Muse."""
    return str(uuid.UUID(int=(int(time.time() * 1000) << 80) | (7 << 76)
                         | (secrets.randbits(12) << 64) | (2 << 62)
                         | secrets.randbits(62)))


class Host:
    def __init__(self, binary, workspace, env, timeout):
        self.timeout = timeout
        self.buffer = bytearray()
        self.responses = {}
        self.proc = subprocess.Popen(
            [binary, "serve", "--disable-shell"], cwd=workspace, env=env,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL, bufsize=0,
        )

    def request(self, method, params, request_id):
        self.send(method, params, request_id)
        if request_id is None:
            return None
        return self.response(method, request_id)

    def send(self, method, params, request_id):
        frame = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            frame["params"] = params
        if request_id is not None:
            frame["id"] = request_id
        self.proc.stdin.write((json.dumps(frame) + "\n").encode())
        self.proc.stdin.flush()

    def response(self, method, request_id):
        deadline = time.monotonic() + self.timeout
        while True:
            if request_id in self.responses:
                frame = self.responses.pop(request_id)
                if "error" in frame:
                    raise RPCError(method, frame["error"])
                return frame["result"]
            while b"\n" not in self.buffer:
                remaining = deadline - time.monotonic()
                if remaining <= 0 or not select.select([self.proc.stdout], [], [], remaining)[0]:
                    raise TimeoutError(f"MSP {method} response timed out")
                chunk = os.read(self.proc.stdout.fileno(), 65536)
                if not chunk:
                    raise RuntimeError(f"MSP closed during {method}")
                self.buffer.extend(chunk)
                if len(self.buffer) > 8 * 1024 * 1024:
                    raise RuntimeError("MSP frame exceeds probe limit")
            raw, _, tail = self.buffer.partition(b"\n")
            self.buffer[:] = tail
            frame = json.loads(raw)
            if frame.get("id") != request_id:
                if frame.get("id") is not None:
                    self.responses[frame["id"]] = frame
                if time.monotonic() >= deadline:
                    raise TimeoutError(f"MSP {method} response timed out")
                continue
            if "error" in frame:
                raise RPCError(method, frame["error"])
            return frame["result"]

    def initialize(self):
        self.request("initialize", {"clientInfo": {"name": "gc_receipt_probe", "version": "1"}}, 1)
        self.request("initialized", None, None)

    def kill(self):
        # Only this probe's direct child is touched; never discover/kill by name.
        if self.proc.poll() is None:
            self.proc.kill()
        self.proc.wait(timeout=5)
        self.proc.stdin.close()
        self.proc.stdout.close()


def events(path, session_id, command_id):
    selected = []
    queue_items = set()
    for line in path.read_text().splitlines():
        row = json.loads(line)
        if row.get("stream", {}).get("id") != session_id:
            continue
        payload = row.get("payload", {})
        kind = row.get("payload_type")
        if kind == "session.resumed":
            selected.append({"sequence": row["sequence"], "type": kind,
                             "resumedFrom": payload.get("record", {}).get("resumed_from_sequence")})
        elif kind in ("runtime.user_intent.accepted", "runtime.user_intent.materialized") and payload.get("intent_id") == command_id:
            selected.append({"sequence": row["sequence"], "type": kind,
                             "durability": row.get("durability"), "commandId": command_id,
                             "sourceSessionId": payload.get("source_session_id"),
                             "deliveryPolicy": payload.get("delivery_policy"),
                             "envelopeSequence": payload.get("envelope_sequence"),
                             "outcome": payload.get("outcome", {}).get("kind"),
                             "startedRecordId": payload.get("outcome", {}).get("run_started_session_record_id")})
        elif kind == "runtime.session" and (payload.get("run_id") == command_id
                or row.get("causation_id") == command_id
                or payload.get("event", {}).get("item_id") in queue_items):
            event = payload.get("event", {})
            if event.get("kind") == "inbox_item_queued":
                queue_items.add(payload.get("source_run_record_id"))
            selected.append({"sequence": row["sequence"], "recordId": row["id"],
                             "type": kind, "commandId": command_id,
                             "runId": payload.get("run_id"), "durability": row.get("durability"),
                             "event": event.get("kind"), "terminal": event.get("terminal"),
                             "reason": event.get("reason"), "queueItemId": event.get("item_id"),
                             "queuedRecordId": payload.get("source_run_record_id") if event.get("kind") == "inbox_item_queued" else None,
                             "deliveryVisible": event.get("delivery_visible"),
                             "causationId": row.get("causation_id")})
        elif kind == "runtime.command_intake.received" and payload.get("record", {}).get("command_id") == command_id:
            selected.append({"sequence": row["sequence"], "type": kind,
                             "durability": row.get("durability"), "commandId": command_id})
        elif kind == "runtime.command_intake.settled" and payload.get("record", {}).get("command_id") == command_id:
            selected.append({"sequence": row["sequence"], "type": kind,
                             "durability": row.get("durability"), "commandId": command_id,
                             "outcome": payload["record"].get("outcome")})
        elif kind == "runtime.queued_turn.reclaim_settled" and payload.get("record", {}).get("command_id") == command_id:
            selected.append({"sequence": row["sequence"], "type": kind,
                             "durability": row.get("durability"), "commandId": command_id,
                             "outcome": payload["record"].get("outcome")})
    return selected


def probe(binary, timeout, summary, queued_crash=False):
    with tempfile.TemporaryDirectory(prefix="gc-muse-admission-") as root:
        root = Path(root)
        workspace = root / "workspace"
        workspace.mkdir()
        home = root / "home"
        home.mkdir()
        # No inherited GC/BEADS routing, credentials, user config, or model keys.
        env = {"PATH": os.environ.get("PATH", ""), "HOME": str(home),
               "TMPDIR": str(root), "LANG": "C.UTF-8",
               "XDG_CONFIG_HOME": str(root / "config"),
               "XDG_DATA_HOME": str(root / "data"),
               "XDG_STATE_HOME": str(root / "state")}
        version = subprocess.run([binary, "--version"], env=env, capture_output=True,
                                 text=True, check=True, timeout=timeout)
        summary["version"] = version.stdout.strip()
        session_id, command_id = uuid7(), uuid7()
        summary.update(sessionId=session_id, commandId=command_id, provider="echo")
        first = Host(binary, workspace, env, timeout)
        try:
            first.initialize()
            session = first.request("session/start", {"commandId": uuid7(),
                "sessionId": session_id, "workspaceRoot": str(workspace), "providerId": "echo"}, 2)["session"]
            if session["sessionId"] != session_id:
                raise RuntimeError("session UUID changed")
            log = Path(session["path"]).resolve()
            if not log.is_relative_to((root / "data").resolve()):
                raise RuntimeError("provider log escaped isolated XDG data")
            params = {"commandId": command_id, "sessionId": session_id,
                      "input": [{"type": "text", "text": "Reply exactly OK."}], "ifBusy": "queue"}
            if queued_crash:
                other_id = uuid7()
                other = dict(params, commandId=other_id)
                first.send("turn/start", params, 3)
                first.send("turn/start", other, 4)
                results = [first.response("turn/start", 3), first.response("turn/start", 4)]
                summary["concurrentReplies"] = [dict({k: r.get(k) for k in ("status", "disposition", "commandId", "turnId")}, submittedCommandId=cid)
                                                 for cid, r in zip((command_id, other_id), results)]
                command_id, result = queued_admission(list(zip((command_id, other_id), results)))
                params["commandId"] = command_id
                summary["commandId"] = command_id
            else:
                result = first.request("turn/start", params, 3)
                correlate_admission(result, command_id)
            summary["initialReply"] = {k: result.get(k) for k in ("status", "disposition", "commandId", "turnId")}
            summary["beforeCrash"] = events(log, session_id, command_id)
            summary["observedCrashPhase"] = "run-started" if any(
                e.get("event") == "started" and e.get("runId") == command_id
                for e in summary["beforeCrash"]) else "accepted-before-observed-start"
            summary["preCrashRunStarted"] = summary["observedCrashPhase"] == "run-started"
            if queued_crash and any((e.get("event") == "started" and e.get("runId") == command_id)
                                    or e.get("outcome") == "top_level_turn_started"
                                    for e in summary["beforeCrash"]):
                raise RuntimeError("queued successor already started before crash")
            accepts_before = [e for e in summary["beforeCrash"]
                              if e["type"] == "runtime.user_intent.accepted"]
            if (result.get("status") != "accepted" or result.get("turnId") != command_id
                    or len(accepts_before) != 1
                    or accepts_before[0].get("durability") != "durable"
                    or accepts_before[0].get("sourceSessionId") != session_id):
                raise RuntimeError("pre-crash durable acceptance assertion failed")
        finally:
            first.kill()
        second = Host(binary, workspace, env, timeout)
        try:
            second.initialize()
            resumed = second.request("session/resume", {"commandId": uuid7(), "sessionId": session_id}, 2)["session"]
            if resumed["sessionId"] != session_id:
                raise RuntimeError("resume UUID changed")
            if Path(resumed["path"]).resolve() != log:
                raise RuntimeError("resume log binding changed")
            summary["resumeStatus"] = resumed.get("status")
            try:
                replay = second.request("turn/start", params, 3)
            except (TimeoutError, RuntimeError) as exc:
                summary["replayTimedOut"] = isinstance(exc, TimeoutError)
                summary["replayResponseWaitSeconds"] = timeout
                summary["processAliveAfterReplayFailure"] = second.proc.poll() is None
                summary["afterResume"] = events(log, session_id, command_id)
                raise
            correlate_admission(replay, command_id)
            summary["replayReply"] = {k: replay.get(k) for k in ("status", "disposition", "commandId", "turnId")}
        finally:
            second.kill()
        summary["afterResume"] = events(log, session_id, command_id)
        accepts = [e for e in summary["afterResume"] if e["type"] == "runtime.user_intent.accepted"]
        if (len(accepts) != 1 or accepts != accepts_before
                or replay.get("status") != "accepted"
                or result.get("turnId") != replay.get("turnId")):
            raise RuntimeError("durable acceptance/replay identity assertion failed")
        if queued_crash and not any(e.get("outcome") == "top_level_turn_started"
                                    for e in summary["afterResume"]):
            raise RuntimeError("queued successor has no materialization after resume")
        summary["admissionReplayPassed"] = True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", default="muse")
    parser.add_argument("--output", type=Path, required=True, help="Sanitized JSON summary; raw logs are removed")
    parser.add_argument("--timeout", type=float, default=20, help="Per-response wait seconds (maximum 60)")
    parser.add_argument("--queued-crash", action="store_true", help="Crash an admitted queued successor; fail if replay stalls")
    args = parser.parse_args()
    if not 0 < args.timeout <= 60:
        parser.error("timeout must be >0 and <=60")
    binary = shutil.which(args.binary)
    if binary is None:
        parser.error("Muse binary not found")
    summary = {"schemaVersion": 1, "admissionReplayPassed": False,
               "limitation": "Acceptance and cached disposition do not prove successful or resumed execution"}
    failed = False
    try:
        summary["scenario"] = "queued-crash" if args.queued_crash else "admission-crash"
        probe(binary, args.timeout, summary, args.queued_crash)
    except (OSError, ValueError, KeyError, RuntimeError, subprocess.SubprocessError) as exc:
        failed = True
        summary["errorType"] = type(exc).__name__
        if isinstance(exc, RPCError):
            summary["rpcError"] = exc.fields
        if isinstance(exc, (RuntimeError, TimeoutError)):
            # These exceptions carry only fixed probe diagnostics or method/code.
            summary["error"] = str(exc)
        # Do not retain arbitrary provider output or environment values.
    args.output.write_text(json.dumps(summary, indent=2) + "\n")
    return 1 if failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
