# Muse admission proof, September 2026

The isolated proof shows why Gas City must reconcile execution outcomes after
admission. It does not fix the live Muse/tmux stall or ship a runtime adapter.
Controller examined: `f3e4f384a`. Tested binary: Muse Code 1.4.0
(`1.4.0-R4302.1`).

The live refinery pane showed twelve reminders without the ready prompt.
Matching live identities rule against a stale-session explanation; the installed
idle predicate refuses newer queued delivery. The pane does not establish its
input mode, absence of active tools, or which original submit failed. Muse is
outside the Claude/Codex submit-verification families. A terminal key receipt
cannot establish durable Muse admission.

## Measured crash windows

| Observed phase before crash | Retained outcome after resume/replay | Evidence and limit |
| --- | --- | --- |
| Actual run started | Historical start reconstructed, orphan cancelled, cached same-command reply | [Post-start fixture](../../scripts/probes/muse-msp-post-start.receipt.json); one acceptance/start in this window, no execution continuation |
| Accepted, no run start observed | terminal_no_effect, then replay records a second acceptance under the same command ID | [Admission fixture](../../scripts/probes/muse-msp-admission.receipt.json); possible valid no-effect re-admission, no duplicate execution observed |
| Accepted queued successor, no start observed | Command abandoned/reclaimed; exact queue item drained invisibly; replay rejected | [Queued fixture](../../scripts/probes/muse-msp-queued-crash.receipt.json); definitive rejection, not unknown transport failure |

The latest admission fixture binds session
`01a0e1b1-8c19-7a10-9e20-018dd32d8033` to command
`01a0e1b1-8c19-7da5-9e0c-55de64a048b8`. Sequence 11 durably accepted the
intent; no run start was observed before crash despite the reply's started
disposition. Sequence 12 resumed; 13 materialized the original envelope as
terminal_no_effect. Replay echoed the exact command ID; sequences 14/15
recorded new intake/accepted settlement, and 17 another intent acceptance.
No execution appeared in this bounded capture. The conservative one-acceptance
assertion failed; that assertion is not the complete MSP recovery contract.

The latest queued fixture binds session
`01a0e1b1-8c35-7704-9e87-b6694e4e8017` to queued command
`01a0e1b1-8c35-776e-82aa-94896d00231d`. Sequence 27 accepted it; 28 queued
it on the incumbent. Restart marked its command abandoned at 30, reclaimed it
at 31, resumed at 32, and drained the exact item at 33 with
delivery_visible=false. Exact-command replay returned commandRejected
(`-32030`), reason abandoned, retryable=false. No successor start was observed.

The generated MSP error schema supports exact command settlement via commandId
and reason; explicit retryable overrides the table default. This is a
definitive rejection. The incumbent failed for missing credentials, so this
was not a healthy busy-model crash test. It still demonstrates acceptance
before execution requiring reconciliation. Neither case proves permanent loss,
paid-provider crash behavior, or the cause of the live stall.

A separate bounded managed-server control consumed three real-provider calls:
distinct commands with identical input were admitted started/queued/queued,
then each emitted turn/started and completed in order. It proves execution and
queue drain in that control, not continuation across a crash. No additional
real-provider calls are needed for this milestone.

## Reproduce without credentials or city input

```sh
python3 scripts/probes/muse-msp-admission.py --binary /absolute/path/to/muse --output /tmp/muse-admission-summary.json
python3 scripts/probes/muse-msp-admission.py --binary /absolute/path/to/muse --queued-crash --timeout 10 --output /tmp/muse-queued-crash-summary.json
python3 -B -m unittest discover -s scripts/probes -p 'test_muse_msp_admission.py'
```

The standard-library probe owns only direct child processes and disposable
HOME/workspace/XDG state. Its environment excludes credentials and GC/BEADS
routing; echo is the only provider choice. Echo execution may fail configuration:
the zero-cost probe tests admission, not successful model work. Raw logs are
deleted; sanitized IDs/outcomes survive. Choose a new output file.

Binary newline framing avoids the TextIO/select buffering pitfall. Each
response wait has an absolute deadline; tiny stdin writes are outside it.
Stderr cannot block an undrained pipe. Initialized notifications omit params.
Responses are paired with submitted commands regardless of arrival order;
distinct turn identities are rejected as outside this bounded proof, never
reused as command IDs. Error summaries preserve allowlisted kinds/reasons,
UUID command IDs, and boolean retry flags, excluding arbitrary message/detail
text. Offline tests cover identity selection and diagnostic filtering.

The crash phase is measured from durable run events, not RPC disposition.
The historical post-start fixture predates echoed-command capture; its exact
stream linkage supports that bounded observation, not general RPC correlation.
The latest fixtures retain echoed command IDs. Each scenario replays once and
exits nonzero when its conservative assertion fails or the required window is
not reached. Nonzero is evidence for review, not provider corruption.

## Remaining integration proof

Prove reconciliation for accepted-but-unstarted, terminal_no_effect, abandoned,
cancelled, and rejected commands before production acknowledgement. Persist the
exact attempt before admission and bind receipts to provider UUID plus Gas City
incarnation. Retain unknown attempts across lease expiry, TTL, supersession,
withdrawal, and restart; keep proven pre-send failures retryable. Test ack-write
failure, identical bodies, stale outcomes, and manual drop. Old binaries may
ignore new retention fields, so versioned state rejection or tested quarantine
is a rollback gate.

Implement one concrete managed-provider consumer behind the existing worker
boundary after these cases pass. Keep tmux the default; add no dormant generic
interfaces or role-specific Go logic. Recurring duties need explicit
reconciliation after failed/no-effect runs; waking a live process is insufficient.
