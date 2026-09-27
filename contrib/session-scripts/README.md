# Session Scripts

Community-maintained session provider scripts for Gas City's exec session
provider. These are real implementations we ship, but they have external
dependencies and aren't the same support tier as `gc` itself.

See [Exec Session Provider](../../docs/reference/exec-session-provider.md)
for the protocol specification.

## Scripts

### gc-session-muse-msp

Opt-in managed Muse transport using MSP instead of terminal input. Requires
Python 3 and Muse Code (isolated transport checks used 1.4.0-R4302.1).
Set `GC_SESSION=exec:/absolute/path/gc-session-muse-msp`,
`GC_MUSE_MSP_DIR` to an owned directory with mode 0700 and a short Unix
socket path, `GC_MUSE_MSP_BIN` to the installed binary, and
`GC_MUSE_MSP_PROVIDER` explicitly (`echo` for credential-free checks).
Set `GC_MUSE_MSP_DATA_HOME` to dedicated persistent Muse data storage;
it changes only the host child's `XDG_DATA_HOME`, preserving authentication
configuration and other runtime providers.
Use a separate managed provider/canary city; this is not a TUI migration.

The detached host retains exact session UUIDs and command receipts across
controller exits. Admission, observed start, and terminal outcome remain
separate. Unknown submissions stay held; the adapter never invents a new
command ID to retry them. Terminal attach, keystrokes, and legacy nudge are
unsupported. See the [receipt contract](../../docs/reference/exec-session-provider.md#managed-command-admission).

Offline checks (no processes, model calls, or network):

```bash
python3 -m unittest discover -s contrib/session-scripts -p test_muse_msp.py
```

### gc-session-screen

GNU screen backend. Creates screen sessions, sends keystrokes for nudge
and interrupt, captures output via `hardcopy`, and stores metadata in
sidecar files.

**Dependencies:** `screen`, `jq`, `bash`

**Usage:**

```bash
export GC_SESSION=exec:/path/to/contrib/session-scripts/gc-session-screen
gc start my-city
```

**Parity with tmux provider:** The script implements the full 13-operation
protocol but does not yet include Gas Town theming (status bar colors,
role emoji, keybindings) or lifecycle features (remain-on-exit, auto-respawn,
zombie detection). See comments in the script header for the full gap list.

### gc-session-k8s (reference — prefer native provider)

Kubernetes backend via exec protocol. Runs each agent session as a K8s
Pod using `kubectl` subprocesses. This script is now a **reference
implementation** — prefer the native K8s provider (`GC_SESSION=k8s` or
`[session] provider = "k8s"`) which uses client-go for direct API calls
and eliminates all subprocess overhead. Pod manifests are compatible
between the two for mixed-mode migration.

**Dependencies:** `kubectl`, `jq`, `bash`

**Usage (legacy):**

```bash
export GC_SESSION=exec:/path/to/contrib/session-scripts/gc-session-k8s
export GC_K8S_IMAGE=myregistry/gc-agent:latest
gc start my-city
```

**Native provider (recommended):**

```bash
export GC_SESSION=k8s
export GC_K8S_IMAGE=myregistry/gc-agent:latest
gc start my-city
```

See [docs/k8s-guide.md](../../docs/k8s-guide.md) for the full setup guide,
K8s manifests, and agent Dockerfile.

## Opt-in recurring managed duty

`gc-duty-reconcile` accepts an explicit JSON target list and a private state
path. It uses the managed host’s `conditional-admit`; it never wakes,
resets, or replays tools. Run it from a city-scoped cooldown order every
30 minutes with a timeout of 60 seconds. Existing order suspension gates
still apply. Use only managed providers advertising message admission.

```json
{"interval_seconds":1800,"targets":[{"session":"explicit-session-id","provider":"muse-managed","message":"Reconcile current durable work.","allow_without_work":false}]}
```

```sh
/path/to/gc-duty-reconcile --config /path/to/targets.json --state /private/mode0700/duty/state.json --city /path/to/city
```

Each immutable command UUID, full request, and receipt stays in the private
state. The host atomically checks the current fence, previous terminal command,
and empty pending set before admission; acceptance does not mean execution.
The default requires open work in `currently_processing_bead_id`; an operator
can explicitly set `allow_without_work:true` for a permanent patrol duty.
Attached, held, inactive, busy, or uncertain sessions are skipped. The
minimum cadence is 30 minutes. An uncertain enqueue is pinned before the
provider call and requires querying the exact retained receipt before removing
its state record; elapsed time never permits automatic retry.

Offline tests:

```sh
python3 -m unittest discover -s contrib/session-scripts -p 'test_duty_reconcile.py'
```
