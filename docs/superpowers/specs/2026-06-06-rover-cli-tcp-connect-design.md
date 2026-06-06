# 设计文档：rover-cli 实时诊断 CLI（首个能力：trace connect）

- 日期：2026-06-06（第二版，根据评审反馈修订）
- 状态：已确认
- 范围：skywalking-rover 新增 `diagnosis` 模块与独立 `rover-cli` 二进制

## 背景与目标

rover 目前作为 daemon 运行，将数据持续上报 OAP 后端，缺少一个面向运维/开发人员的**按需、实时**诊断入口。本设计新增：

1. daemon 侧新模块 `diagnosis`：暴露 gRPC 控制接口，承载按需实时诊断任务；
2. 独立 CLI 二进制 `rover-cli`：与本机或远程机器上的 rover daemon 通信，先通过
   process_discovery 已发现的进程信息筛选定位目标进程（service / instance /
   process name 或 pid），再对目标进程发起实时监控；
3. 首个监控能力 **trace connect**：实时观测目标进程对外发起 TCP 连接的
   成功 / 失败情况，用于排查"某个进程连接其他服务是否正常"。
   后续将在同一框架下扩展 trace dns、trace http 及 profiling 类能力。

### 已确认的关键决策

| 决策点 | 结论 |
|---|---|
| 通信架构 | CLI ↔ rover daemon（daemon 新增 gRPC 控制接口），由 daemon 执行 eBPF 监控 |
| CLI 形态 | 独立新二进制 `rover-cli`，与 daemon 同镜像分发 |
| 控制接口协议 | gRPC + TCP 端口，事件用 server-streaming 推送 |
| 默认开关与绑定 | 默认开启，仅绑定 `127.0.0.1:12700`；远程场景由用户改 host 配置 |
| 安全 | 探索阶段不做认证 / TLS |
| 模块名 | `diagnosis` |
| 默认端口 | `12700`（避开 11800 OAP gRPC、6060 pprof） |
| 功能命名 | 全链路统一 `trace connect`：BPF `bpf/diagnosis/trace/connect.c`、Go 包 `pkg/diagnosis/trace/connect`、RPC `TraceConnect`、CLI `rover-cli trace connect`；未来 trace dns / trace http 同层级扩展 |
| BPF 实现 | 新建独立轻量 BPF（pid 白名单过滤），不复用/不依赖 accesslog 模块；不挂 `kprobe/tcp_connect`，远端地址在 C 内从 `sys_enter_connect` 的 sockaddr 参数解析为 v4/v6/port 分字段 |
| BPF 文件位置 | 所有 BPF C 程序统一放根目录 `bpf/` 下（本功能为 `bpf/diagnosis/trace/connect.c`）；bpf2go 生成的 Go 绑定按项目惯例放对应 Go 包内 |
| proto 位置 | 根目录 `proto/diagnosis/diagnosis.proto`，后续其他 proto 也集中在 `proto/` 下；生成的 `*.pb.go` 加入 `.gitignore`，由 `make proto-gen`（pure-Go buf，无需安装 protoc）生成 |
| 进程筛选输入 | 通用 `ProcessSelector` message（service_name / instance_name / process_name 子串匹配 + pids），未来诊断任务复用 |
| 进程可见性 | 使用 `GetAllProcesses()`：本地已发现即可见、可监控，**不要求已上报 OAP 后端**；`ProcessInfo.reported_to_backend` 字段标识是否已同步到后端（依据 `ID() != ""`） |
| 名称多匹配行为 | 匹配到的进程全部监控，事件输出标注 pid |
| 任务生命周期 | 随 CLI 连接存亡：RPC 断开（Ctrl+C / --duration 到期 / 断网）即停止并卸载 BPF |
| 事件时间戳 | BPF 上报 ktime（boot 单调时间），server 端用现有 `host.Time()`（`pkg/tools/host/time.go`）换算为真实墙钟时间 |
| 结果呈现 | 实时流式逐条输出 + 结束时按远端地址分组的统计汇总 |
| 输出格式 | `process list`：table / json / yaml；`trace connect`：text / json / yaml（yaml 便于 e2e 验证） |
| 统计聚合位置 | CLI 端本地聚合，daemon 只推原始事件、保持无状态 |
| 代码复用 | IP 解析复用 `ip.ParseIPV4` / `ip.ParseIPV6`；事件读取实现 `btf.EventReader`（`ReadFrom(btf.Reader)`）；时间换算复用 `host.Time()` |
| CLI 目录 | `cmd/cli/`（binary 名 `rover-cli`）；同时将 `cmd/roverd.go` 调整为 `cmd/roverd/main.go` |
| 测试 | 单元测试 + 自动化 e2e（`test/e2e/cases/diagnosis/`，参照现有 kind 体系）+ 手动验证清单 |

## 1. `diagnosis` 模块（daemon 侧）

位置：`pkg/diagnosis/`，实现 `module.Module` 接口（`pkg/module/module.go`），
注册到 `pkg/boot/register.go`。

- 职责：启动 gRPC server，提供进程查询与实时诊断任务接口。
- 依赖模块：`process_discovery`（通过 `GetAllProcesses()` 查询本地已发现进程）。
- 生命周期：`Start` 时监听配置的 host:port；`Shutdown` 时关闭 server（取消所有
  进行中的监控 stream，触发各任务卸载 BPF）。

配置段（`configs/rover_configs.yaml` 新增）：

```yaml
diagnosis:
  # Is active the diagnosis gRPC server
  active: ${ROVER_DIAGNOSIS_ACTIVE:true}
  # The bind host of the diagnosis gRPC server
  host: ${ROVER_DIAGNOSIS_HOST:127.0.0.1}
  # The bind port of the diagnosis gRPC server
  port: ${ROVER_DIAGNOSIS_PORT:12700}
```

## 2. gRPC 接口定义

proto 文件位于 `proto/diagnosis/diagnosis.proto`，go_package 为
`github.com/apache/skywalking-rover/proto/diagnosis`。生成代码输出到同目录，
**不入库**（`.gitignore`），由 `make proto-gen` 生成（buf + protoc-gen-go /
protoc-gen-go-grpc，全部通过 go 工具链获取，无需系统安装 protoc）。

```proto
syntax = "proto3";

package skywalking.rover.diagnosis.v1;

option go_package = "github.com/apache/skywalking-rover/proto/diagnosis";

service DiagnosisService {
  // 按条件查询 process_discovery 已发现的进程（不要求已上报后端）
  rpc ListProcesses(ListProcessesRequest) returns (ListProcessesResponse) {}
  // 实时跟踪 TCP connect，server-streaming，RPC 断开即停止任务
  rpc TraceConnect(TraceConnectRequest) returns (stream ConnectEvent) {}
}

// 所有诊断任务共用的进程筛选条件，
// 名称为子串匹配，非空条件与 pids 之间取交集（AND）
message ProcessSelector {
  string service_name = 1;
  string instance_name = 2;
  string process_name = 3;
  repeated int32 pids = 4;
}

message ListProcessesRequest {
  ProcessSelector selector = 1;
}

message ListProcessesResponse {
  repeated ProcessInfo processes = 1;
}

message ProcessInfo {
  int32 pid = 1;
  // 后端分配的实体 ID，未上报时为空
  string entity_id = 2;
  string layer = 3;
  string service_name = 4;
  string instance_name = 5;
  string process_name = 6;
  repeated string labels = 7;
  string exe_path = 8;
  string command_line = 9;
  // 是否已成功上报到 OAP 后端
  bool reported_to_backend = 10;
}

message TraceConnectRequest {
  ProcessSelector selector = 1;
}

message ConnectEvent {
  // 真实墙钟时间（server 端由 BPF ktime 换算）
  int64 timestamp_ns = 1;
  int32 pid = 2;
  string process_name = 3;
  // 格式："ip:port"（IPv4）或 "[ip]:port"（IPv6）
  string remote_address = 4;
  bool success = 5;
  // 失败时的错误码（errno）
  int32 errno = 6;
  // connect 调用耗时
  int64 latency_ns = 7;
  // errno 的可读信息，由 daemon 端解析（errno 数值表与 OS/架构相关，
  // 必须在事件产生侧转换），如 "ECONNREFUSED: connection refused"，成功时为空
  string error_message = 8;
}
```

错误处理约定：

- 请求未匹配到任何进程：返回 gRPC `NotFound`；
- BPF 加载失败（内核不支持等）：返回 gRPC `Internal`，附带原因；
- daemon 关闭时主动结束 stream。

## 3. 轻量 BPF：trace connect 探测

C 程序位于 `bpf/diagnosis/trace/connect.c`（所有 BPF C 程序统一在根目录
`bpf/` 下，按 模块/命令层级 组织子目录），参考
`bpf/accesslog/syscalls/connect.c` 的成熟写法，大幅精简，与 accesslog 互不影响。

- 挂载点：
  - `tracepoint/syscalls/sys_enter_connect`：pid 白名单过滤后记录 sockaddr
    指针与起始 ktime；
  - `tracepoint/syscalls/sys_exit_connect`：取返回值判定成功 / 失败
    （`-EINPROGRESS` 视为成功），在 C 内解析 sockaddr 为
    `remote_addr_v4(u32) / remote_addr_v6([16]u8) / remote_port(u32)`
    分字段（与 accesslog `connection.h` 同约定），连同
    start/end ktime、errno 经 perf event buffer 输出。
- 过滤：`diagnosis_trace_connect_pids` hash map 做 pid 白名单，
  **只跟踪指定 pid**，其余进程在入口处即返回，开销最小化。
- 仅上报 `AF_INET` / `AF_INET6` 的 connect。

Go 侧任务实现：`pkg/diagnosis/trace/connect/`（runner + 事件定义 +
bpf2go 生成的 Go 绑定，遵循项目惯例生成到 Go 包内）。

- 事件结构实现 `btf.EventReader`（`ReadFrom(btf.Reader)`），地址解析复用
  `ip.ParseIPV4` / `ip.ParseIPV6`，时间换算复用 `host.Time()`；
- 每个 TraceConnect RPC 会话独立加载一份 BPF、写入自己的 pid 集合；
- RPC 断开即卸载 BPF、清理 map 与 perf reader；
- 并发多个 CLI 会话互不影响。

## 4. rover-cli（独立二进制）

位置：`cmd/cli/`（binary 名 `rover-cli`），使用 cobra。
同时将现有 `cmd/roverd.go` 移至 `cmd/roverd/main.go`，两个二进制并列。

```bash
# 第一步：探索进程（不要求进程已上报后端，REPORTED 列展示上报状态）
rover-cli process list [--service x] [--instance x] [--process x] \
    [--addr 127.0.0.1:12700] [--format table|json|yaml]

# 第二步：跟踪（名称或 pid 均可，名称多匹配则全部监控并标注 pid）
rover-cli trace connect [--service x | --instance x | --process x | --pid N[,N..]] \
    [--duration 60s] [--addr 127.0.0.1:12700] [--format text|json|yaml]
```

- `--addr` 默认 `127.0.0.1:12700`，连接远程机器时显式指定；
- `--duration` 可选，到期 CLI 主动结束；不指定则持续运行至 Ctrl+C；
- 统计汇总在 CLI 端聚合，结束时输出。

text 模式输出示例：

```
TIME             PID    PROCESS   REMOTE              RESULT    LATENCY
15:04:05.123     1234   envoy     10.96.0.10:9090     SUCCESS   1.2ms
15:04:06.456     1234   envoy     10.96.0.11:443      REFUSED   0.3ms
^C
--- tcp connect statistics ---
REMOTE              TOTAL  SUCCESS  FAILED  FAIL%
10.96.0.10:9090     12     12       0       0%
10.96.0.11:443      5      0        5       100%
```

- `--format json`：逐行输出 JSON 事件，结束时输出一条统计 JSON；
- `--format yaml`：事件以 `---` 分隔的 YAML 文档流输出，统计同为 YAML
  （服务于 e2e 验证与人类可读的折中）。

## 5. 构建与镜像

- `scripts/build/build.mk`：daemon 构建路径改为 `./cmd/roverd`；
  新增编译 `./cmd/cli`，产出 `bin/rover-cli-$(VERSION)-linux-$(ARCH)`；
- `make proto-gen` 纳入构建前置（生成 proto 代码后才可编译）；
- `docker/Dockerfile.build`：`COPY --from=build /src/bin/rover-cli /rover-cli`，
  与 daemon 同镜像，进容器即可执行 `/rover-cli process list`。

## 6. 测试

- 单元测试：进程筛选匹配逻辑、事件地址/时间解析、CLI 输出格式化
  （table/text/json/yaml）、统计聚合；
- 自动化 e2e：`test/e2e/cases/diagnosis/`，参照 `cases/process/istio` 的
  kind 体系：部署 OAP + rover + 演示服务后，
  1. 复用 process 验证确认进程发现正常；
  2. `kubectl exec` rover pod 执行 `/rover-cli process list --format yaml`，
     用 expected 模板验证；
  3. 对持续发起对外连接的进程执行
     `/rover-cli trace connect --duration 10s --format yaml`，验证输出含
     成功连接事件；
  4. 新 case 注册到 GitHub Actions e2e workflow 矩阵；
- 手动验证清单：成功 / 失败 / Ctrl+C 生命周期 / 多匹配 / NotFound 场景，
  作为开发自查。

## 范围外（本次不做）

- 认证 / TLS；注意：在 `hostNetwork: true` 下 `127.0.0.1` 是节点 loopback、并非
  Pod 隔离，未认证接口在节点本地可达。生产部署需在节点层面限制访问，后续应加
  token/mTLS 或文件权限保护的 unix socket（见配置文档安全说明）；
- 后台任务管理（任务 ID、断开重连查看）；
- trace connect 之外的诊断类型（trace dns、trace http、profiling 类能力等，
  后续在 diagnosis 模块与 `ProcessSelector` 框架下扩展）；
- 诊断结果上报 OAP；
- 非 Kubernetes 的进程发现方式。
