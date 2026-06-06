# Diagnosis CLI

`rover-cli` is a command line tool for on-demand, real-time diagnosis of the
processes that rover has discovered. It ships inside the rover docker image and
talks to the [Diagnosis](configuration/diagnosis.md) gRPC server, so it can run
either inside a rover pod (local) or against a reachable rover on another machine.

```bash
# inside a rover pod
kubectl exec -n <namespace> <rover-pod> -- /rover-cli <command> [flags]
```

The tool has two command groups today: `process` to explore the discovered
processes, and `trace` to observe their runtime behavior in real time. The
`trace` group is designed to grow (e.g. DNS or HTTP tracing) on top of the same
process-selection flags described below.

## Global Flags

These flags apply to every command:

| Flag        | Default            | Description                                                                                       |
|-------------|--------------------|---------------------------------------------------------------------------------------------------|
| `--addr`    | `127.0.0.1:12700`  | Address of the diagnosis gRPC server. Use the target node's address for a remote rover.           |
| `--timeout` | `10s`              | Timeout for connecting to the server (e.g. `5s`, `1m`). It also bounds the `process list` request, but **not** the `trace` collecting duration. An unreachable server fails fast instead of hanging. |

## Process Selection

Both `process list` and `trace connect` select processes with the same set of
filters. Name filters are **substring** matches; when combined they are ANDed,
and pids further intersect the result.

| Flag         | Description                                  |
|--------------|----------------------------------------------|
| `--service`  | Filter by service name (substring).          |
| `--instance` | Filter by instance name (substring).         |
| `--process`  | Filter by process name (substring).          |
| `--pid`      | Filter by process PID(s), comma-separated.   |

`process list` may be called with no filter (it then returns every discovered
process). `trace connect` requires at least one filter — tracing every process
at once is rejected.

> rover is a DaemonSet: each rover pod only sees the processes on **its own
> node**. To inspect a specific workload, exec into the rover pod that runs on
> the same node as that workload.

## process list

List the processes rover has discovered, with their entity metadata.

```bash
/rover-cli process list --service gateway
```

Processes are visible and traceable **even before they are reported to the OAP
backend**. The `REPORTED` column (`reportedToBackend` in structured output)
shows whether the backend has registered the process; `entityId` is empty until
then.

Flags: the [process selection](#process-selection) flags above, plus:

| Flag       | Default | Description                                  |
|------------|---------|----------------------------------------------|
| `--format` | `table` | Output format: `table`, `json`, or `yaml`.   |

Table output:

```
PID      LAYER    SERVICE                   INSTANCE          PROCESS   LABELS            REPORTED
3958377  MESH     e2e::productpage.default  productpage-v1-x  gunicorn  mesh-application  true
3958415  MESH_DP  e2e::productpage.default  productpage-v1-x  envoy     mesh-envoy        true
```

`--format json` prints one JSON object per process; `--format yaml` prints a
single YAML array — both carry the full field set: `pid`, `entityId`, `layer`,
`serviceName`, `instanceName`, `processName`, `labels`, `exePath`,
`commandLine`, `reportedToBackend`.

## trace connect

Stream real-time TCP connect operations issued by the matched processes, then
print per-remote statistics when the session ends. Use it to check whether a
process can successfully reach the services it depends on.

```bash
# stop automatically after 60s
/rover-cli trace connect --service gateway --duration 60s

# trace until interrupted with Ctrl+C
/rover-cli trace connect --pid 1234
```

Flags: the [process selection](#process-selection) flags above, plus:

| Flag         | Default | Description                                                                                  |
|--------------|---------|----------------------------------------------------------------------------------------------|
| `--duration` | (unset) | How long to collect events (e.g. `30s`, `5m`, `1h`). Automatically stops when reached; if unset, traces until interrupted (Ctrl+C). |
| `--format`   | `text`  | Output format: `text`, `json`, or `yaml`.                                                    |

Text output streams one line per connect event and prints a summary at the end:

```
TIME            PID      PROCESS   REMOTE              RESULT             LATENCY
15:04:05.123    1234     envoy     10.96.0.10:9090     SUCCESS            1.2ms
15:04:06.456    1234     envoy     10.96.0.11:443      FAILED(ECONNREFUSED)  0.3ms
^C
--- tcp connect statistics ---
REMOTE              TOTAL  SUCCESS  FAILED  FAIL%
10.96.0.10:9090     12     12       0       0%
10.96.0.11:443      5      0        5       100%
```

Each event carries: `timestampNs`, `pid`, `processName`, `remoteAddress`
(`ip:port` for IPv4, `[ip]:port` for IPv6), `success`, `errno`, `errorMessage`,
`latencyNs`.

- **`errorMessage`** is the human-readable form of `errno`, resolved on the
  daemon side (the errno table is OS/architecture dependent), e.g.
  `ECONNREFUSED: connection refused`. It is empty on success.
- A connect that returns `EINPROGRESS` (non-blocking socket) is reported as a
  success.

When matching multiple processes (e.g. several replicas of a service), every
matched process is traced and each event is labelled with its `pid`.

### Structured output

`--format json` emits one JSON document per event followed by a final
statistics document; `--format yaml` emits the same as a stream of `---`
separated YAML documents. The int64 fields (`timestampNs`, `latencyNs`) are
serialized as JSON strings per the proto3 JSON mapping, while `pid`/`errno`
(int32) are numbers.

## Notes

- The diagnosis server has no authentication yet; see the security note in the
  [Diagnosis](configuration/diagnosis.md) configuration document before exposing
  it beyond the local node.
- A tracing task loads its own eBPF program for the duration of the session and
  unloads it when the session ends (duration reached, Ctrl+C, or the connection
  drops).
