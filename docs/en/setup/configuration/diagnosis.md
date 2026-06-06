# Diagnosis

The diagnosis module provides a gRPC server that allows the `rover-cli` tool to explore discovered processes and run real-time diagnosis tasks,
such as tracing TCP connect operations of target processes.

By default, the server binds to `127.0.0.1` (localhost only). Update the `diagnosis.host` configuration to allow remote access.

> **Security note:** the diagnosis server currently has no authentication or TLS.
> When rover runs with `hostNetwork: true` (the usual DaemonSet setup), `127.0.0.1`
> is the **node's** loopback, shared by all host-network workloads on that node — so
> any local actor that reaches the port can trace/inspect any process on the node.
> This is acceptable for the current exploratory stage; for production, restrict
> access at the node level and prefer a future authenticated transport (token/mTLS
> or a file-permission-gated unix socket). Do not bind it to a routable address.

## Configuration

| Name               | Default     | Environment Key          | Description                                   |
|--------------------|-------------|--------------------------|-----------------------------------------------|
| `diagnosis.active` | `true`      | `ROVER_DIAGNOSIS_ACTIVE` | Enable the diagnosis module.                  |
| `diagnosis.host`   | `127.0.0.1` | `ROVER_DIAGNOSIS_HOST`   | The host address the gRPC server binds to.    |
| `diagnosis.port`   | `12700`     | `ROVER_DIAGNOSIS_PORT`   | The port the gRPC server listens on.          |

## rover-cli

The diagnosis server is consumed by the `rover-cli` command line tool, which ships
inside the rover docker image. For its commands, flags and output formats, see the
[Diagnosis CLI](../diagnosis-cli.md) document.
