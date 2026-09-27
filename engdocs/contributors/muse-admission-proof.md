# Muse admission proof, September 2026

This milestone proves a supported admission path in isolation. It does not fix
the live Muse/tmux stall or deploy a new runtime provider.

The controller examined was `f3e4f384a`; the tested binary was
Muse Code 1.4.0 (`1.4.0-R4302.1`). The refinery's current pane showed
twelve reminders without the configured/default ready prompt. Matching live
session identities rule against a stale-session explanation; the installed
idle predicate refuses newer queued delivery. The pane does not prove its input
mode, absence of active tools, or which original submit failed. Muse is outside
the existing Claude/Codex submit-verification families. Terminal key acceptance
therefore cannot establish durable Muse admission.

## Reproduce without credentials or city input

```sh
python3 scripts/probes/muse-msp-admission.py --binary /absolute/path/to/muse --output /tmp/muse-admission-summary.json
```

The standard-library probe launches only its own `muse serve --disable-shell`
children with isolated HOME, workspace, XDG config/data/state, and temporary
files. Its environment allowlist excludes inherited GC/BEADS routing and model
credentials. It requests the echo provider; there is no paid-provider option.
This zero-cost reproduction tests admission only: echo run configuration may
fail, and no successful model execution is required or claimed.
It records the installed version and sanitized IDs/event outcomes, then removes
raw logs. Do not point its output at an existing valuable file. This is an
explicit process/crash probe, not part of the fast test baseline.

The client reads newline frames with binary `os.read`. Combining TextIO reads
with `select` previously hid an already-buffered response and produced a false
session/start timeout. Each process has its own buffer and each response wait
has an absolute deadline. Stderr is discarded so an undrained pipe cannot block the
host. Cleanup kills only direct children started by the probe.
Use the demonstrated initialize client name `gc_receipt_probe`: the initial
reproducer's hyphenated name was rejected with MSP error `-32602`.

## Measured crash result

The checked-in receipt uses session
`01a0e19e-67eb-7457-94ff-ed2f2bb1c884` and command/intent/run
`01a0e19e-67ec-7715-9a33-1557b19d3d03`. Its captured event summaries show:

- Sequence 11: durable acceptance of that intent in that session.
- Sequence 12: the same run started before the host was killed.
- Sequence 13: replacement host resumed from sequence 12.
- Sequence 14: materialization linked acceptance at 11 to the existing start
  record at 12; it did not start another run after restart.
- Sequence 15: the run ended cancelled with
  `resume_reconcile:orphaned_by_process_loss`.

Reissuing the same command returned the original turn ID and cached
accepted/started disposition. Only one acceptance and one start were recorded.
A separate admission probe distinguished identical prompt bodies by different
command IDs. The final bounded managed-server control used three real-provider
calls, all with identical harmless input and distinct command UUIDs. Its
admission replies were started, queued, queued. Each turn then emitted
turn/started and completed in order. In the exact session stream, accepted
intents appeared at sequences 23, 24, 26, materialization at 29, 91, 145, and
completed terminals at 86, 140, 194. This establishes that managed MSP can
execute and drain a queue without another nudge under those tested conditions;
it does not establish continuation across a crash. No more real-provider calls
are needed for this milestone.

Thus admission survives this tested process replacement and replay is deduped.
The accepted reply is neither a fresh execution receipt nor successful work.
An accepted-but-not-started or cancelled duty must remain observable and reach
explicit reconciliation. No blanket guarantee follows for crashes before
acceptance, lost responses, busy queues, different payloads reusing one ID, or
all Muse versions. The committed credential-free reproduction also passed with
the same binary; its sanitized result is in
[`muse-msp-admission.receipt.json`](../../scripts/probes/muse-msp-admission.receipt.json).
These proofs are not production rollout evidence for a Gas City adapter.

## Queued-crash gate failed

The started-crash reproduction covers a narrower window than acceptance before
execution. A separate zero-cost queued-successor test exposed an unresolved
case on the same binary. Its sanitized
[`queued-crash receipt`](../../scripts/probes/muse-msp-queued-crash.receipt.json)
binds session `01a0e1a2-d3f1-7559-9394-9b91224db551` to actual queued command
`01a0e1a2-d3f1-7559-9394-9b923c7e42e1`:

- Sequence 27 durably accepted the successor with after-current-terminal
  delivery; sequence 28 queued its item on the incumbent run.
- After process replacement, sequence 30 marked its command abandoned because
  no command outcome was committed; sequence 31 reclaimed the queued turn.
- Sequence 33 drained that exact queue item with delivery_visible=false.
- A further resume was idle. Replaying the actual queued command timed out
  while the server stayed alive; no new command event, materialization, or
  successor start appeared through sequence 34.

The incumbent had failed for missing credentials before successor acceptance.
That does not negate the durable accepted-but-unstarted successor. This is a
bounded failure observation, not proof of permanent loss, paid-provider
behavior, or the cause of the original live stall. It blocks treating every
accepted envelope as a proven recoverable obligation.
The from-scratch checked-in probe reached the same queued admission window but
its one replay returned MSP error `-32030` rather than timing out. Both negative
observations are retained in the fixture; neither establishes permanent loss.

To attempt the same window without credentials:

```sh
python3 scripts/probes/muse-msp-admission.py --binary /absolute/path/to/muse --queued-crash --output /tmp/muse-queued-crash-summary.json
```

The probe sends two distinct command IDs, selects the reply actually marked
queued regardless of RPC order, kills its own host, resumes, and replays that
queued command once. It exits nonzero for timeout, absent materialization, or
failure to reach the intended queue window. No automatic retry or real-provider
option is supplied. Fix the provider's command-commit/reclaim contract and
repeat this gate before implementing production admission acknowledgement.

## Queue and rollback gates

An isolated queue prototype reproduced lease replay and TTL deletion of an
uncertain attempt. Retention also needs coverage for supersession, wait
withdrawal, manual drop, stale receipts, identical bodies, and ack-write failure.
These findings are proof requirements, not shipped runtime behavior.

A future sender must persist the exact attempt before possible admission.
Unknown delivery must survive lease expiry, TTL, and controller restart without
automatic paste. Proven pre-send failure must remain retryable. Receipts must
match the immutable command, provider session UUID, and Gas City incarnation;
timestamp, PID, or prompt text alone is insufficient. Acknowledging admission
must preserve a separate record for eventual execution/terminal reconciliation.
An old binary that ignores new retention fields can replay them: versioned
state rejection or a tested quarantine/migration is required before rollback.

## Next implementation slice

Prove a concrete managed Muse provider's launch/resume and execution contract
against the same isolated fixture, including response loss, queued admission,
and cancellation recovery. Then implement its admission path behind the
existing worker boundary with persisted identities and one command per attempt.
Keep tmux the default. Do not add a generic admission interface, unused client,
or wrapper hierarchy before a real consumer exists. Recurring duty scheduling
must distinguish admission from execution and invoke reconciliation after a
failed/cancelled run; waking a live process is insufficient.
