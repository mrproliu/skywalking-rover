# rover-cli 实时诊断 CLI 实施计划（第二版）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**目标：** 新增 daemon 侧 `diagnosis` 模块（gRPC 控制接口，默认 `127.0.0.1:12700`）与独立 CLI 二进制 `rover-cli`，支持按 service/instance/process name 或 pid 筛选 process_discovery 已发现的进程（不要求已上报后端），并对目标进程实时跟踪 TCP connect 成功/失败（`rover-cli trace connect`）。

**架构：** rover daemon 新增 `diagnosis` 模块暴露 gRPC server（`ListProcesses` + `TraceConnect` server-streaming）；跟踪任务按需加载独立轻量 BPF（pid 白名单过滤 `connect` 系统调用 tracepoint，C 内解析 sockaddr 为分字段），随 RPC 连接存亡；rover-cli 为纯 gRPC 客户端，统计聚合在 CLI 端完成。

**技术栈：** Go + cobra + gRPC/protobuf（buf 生成，不入库）+ cilium/ebpf（bpf2go）+ 项目内 `pkg/tools/btf`、`pkg/tools/ip`、`pkg/tools/host` 工具链。

**对应设计文档：** `docs/superpowers/specs/2026-06-06-rover-cli-tcp-connect-design.md`（第二版）

## 运行前提（写入文档）

- 进程发现本身不要求 OAP 后端在线（使用 `GetAllProcesses()`），但 `entity_id` / `reported_to_backend` 仅在上报成功后有值。
- 跟踪任务需要 daemon 具备加载 eBPF 的权限（与现有 profiling 一致）。

## 文件结构总览

```
proto/diagnosis/diagnosis.proto             # proto 定义（根目录 proto/ 下集中管理）
proto/diagnosis/diagnosis.pb.go             # buf 生成，gitignore，不入库
proto/diagnosis/diagnosis_grpc.pb.go        # buf 生成，gitignore，不入库
buf.gen.yaml                                # buf 生成配置（仓库根目录）
pkg/diagnosis/matcher/matcher.go            # 进程筛选逻辑（独立子包，无 BPF 依赖）
pkg/diagnosis/matcher/matcher_test.go
pkg/diagnosis/config.go                     # 模块配置
pkg/diagnosis/module.go                     # 模块生命周期 + gRPC server
pkg/diagnosis/server.go                     # DiagnosisService 实现
pkg/diagnosis/server_test.go
pkg/diagnosis/trace/connect/runner.go       # BPF 加载/卸载/事件通道
pkg/diagnosis/trace/connect/event.go        # 事件结构 + 地址/时间解析
pkg/diagnosis/trace/connect/event_test.go
pkg/diagnosis/trace/connect/bpf_*_bpfel.go  # bpf2go 生成（按项目惯例入库）
bpf/diagnosis/trace/connect.c               # BPF 程序（所有 BPF C 文件统一在根 bpf/ 下）
cmd/roverd/main.go                          # daemon 入口（由 cmd/roverd.go 移入）
cmd/cli/main.go                             # rover-cli 入口
cmd/cli/root.go                             # 根命令 + 全局 flag
cmd/cli/process.go                          # process list 命令
cmd/cli/trace.go                            # trace connect 命令
cmd/cli/output.go                           # 输出格式化（table/text/json/yaml）
cmd/cli/output_test.go
cmd/cli/stats.go                            # 统计聚合
cmd/cli/stats_test.go
configs/rover_configs.yaml                  # 修改：新增 diagnosis 配置段
pkg/boot/register.go                        # 修改：注册 diagnosis 模块
scripts/build/build.mk                      # 修改：roverd 路径调整 + 编译 rover-cli
scripts/build/generate.mk                   # 修改：新增 proto-gen target
scripts/build/test.mk / lint.mk             # 修改：test/lint 依赖 proto-gen
.gitignore                                  # 修改：忽略 proto/**/*.pb.go
docker/Dockerfile.build                     # 修改：镜像加入 rover-cli
docs/en/setup/configuration/diagnosis.md    # 新增配置文档
test/e2e/cases/diagnosis/                   # 新增自动化 e2e 用例
.github/workflows/rover.yaml                # 修改：e2e matrix 注册新用例
```

**通用约定：**
- 所有新建 `.go` / `.c` / `.proto` / `.yaml` / `.md` 文件头部都要带项目标准 Apache License 头（从同类型现有文件复制对应注释风格）。
- 错误变量命名优先用 `err`，仅在遮蔽时加前缀。
- 每个 Task 完成即 commit；commit 前跑 `make lint`。
- 本机为 macOS：编译校验用 `GOOS=linux go build ./...`；纯逻辑包（matcher、cmd/cli）的测试可直接本机 `go test`；BPF 生成用 `make container-generate`；涉及 BPF 依赖的包测试在 linux 环境跑（CI 兜底）。

---

### Task 1: proto 定义与 buf 生成体系

**Files:**
- Create: `proto/diagnosis/diagnosis.proto`
- Create: `buf.gen.yaml`
- Modify: `scripts/build/generate.mk`
- Modify: `.gitignore`

- [ ] **Step 1: 编写 proto 文件**

`proto/diagnosis/diagnosis.proto`（带 Apache License `//` 注释头）：

```proto
syntax = "proto3";

package skywalking.rover.diagnosis.v1;

option go_package = "github.com/apache/skywalking-rover/proto/diagnosis";

service DiagnosisService {
  // ListProcesses query the processes discovered by the process_discovery module,
  // the processes are visible even if they have not been reported to the backend
  rpc ListProcesses(ListProcessesRequest) returns (ListProcessesResponse) {}
  // TraceConnect trace the TCP connect operations of the matched processes in real-time,
  // the tracing task would be stopped when the RPC stream is closed
  rpc TraceConnect(TraceConnectRequest) returns (stream ConnectEvent) {}
}

// ProcessSelector is the generic process filter shared by all diagnosis tasks,
// the name conditions are substring matching, all non-empty conditions and pids
// are combined with AND
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
  // the entity id assigned by the backend, empty if not reported yet
  string entity_id = 2;
  string layer = 3;
  string service_name = 4;
  string instance_name = 5;
  string process_name = 6;
  repeated string labels = 7;
  string exe_path = 8;
  string command_line = 9;
  // whether the process has been reported to the OAP backend successfully
  bool reported_to_backend = 10;
}

message TraceConnectRequest {
  ProcessSelector selector = 1;
}

message ConnectEvent {
  // the real wall-clock time converted from the BPF ktime
  int64 timestamp_ns = 1;
  int32 pid = 2;
  string process_name = 3;
  // format: "ip:port" for IPv4, "[ip]:port" for IPv6
  string remote_address = 4;
  bool success = 5;
  // the errno when connect failure
  int32 errno = 6;
  // the duration of the connect syscall
  int64 latency_ns = 7;
}
```

- [ ] **Step 2: 编写 buf.gen.yaml**

仓库根目录 `buf.gen.yaml`（带 `#` License 头）：

```yaml
version: v2
plugins:
  - local: protoc-gen-go
    out: proto
    opt: paths=source_relative
  - local: protoc-gen-go-grpc
    out: proto
    opt: paths=source_relative
```

- [ ] **Step 3: generate.mk 新增 proto-gen target**

`scripts/build/generate.mk` 末尾追加（buf 与插件均走 go 工具链，无需系统安装 protoc，容器与 CI 通用）：

```makefile
.PHONY: proto-gen
proto-gen:
	$(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.6
	$(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
	PATH="$(shell $(GO) env GOPATH)/bin:$$PATH" $(GO) run github.com/bufbuild/buf/cmd/buf@v1.50.0 generate proto
```

注意：`$(GO)` 等变量名以 `scripts/build/base.mk` 中的实际定义为准（若 base.mk 用 `GO_BUILD`/`GO_GET` 风格而无 `$(GO)`，直接用 `go`）。

- [ ] **Step 4: .gitignore 忽略生成物**

`.gitignore` 追加：

```
proto/**/*.pb.go
```

- [ ] **Step 5: 生成并验证编译**

```bash
make proto-gen
GOOS=linux go build ./proto/...
go mod tidy
git status   # 确认 *.pb.go 不在未跟踪列表中（被 ignore）
```

预期：生成 `proto/diagnosis/diagnosis.pb.go` 与 `diagnosis_grpc.pb.go`，编译通过，且 git 不跟踪生成物。

- [ ] **Step 6: Commit**

```bash
git add proto/diagnosis/diagnosis.proto buf.gen.yaml scripts/build/generate.mk .gitignore go.mod go.sum
git commit -m "feat(diagnosis): add diagnosis proto definition with buf generation"
```

---

### Task 2: cmd 目录重组（roverd 入口迁移）

**Files:**
- Move: `cmd/roverd.go` → `cmd/roverd/main.go`
- Modify: `scripts/build/build.mk`

- [ ] **Step 1: 迁移 daemon 入口**

```bash
mkdir -p cmd/roverd
git mv cmd/roverd.go cmd/roverd/main.go
```

文件内容不变（package main，调用 `cmd.NewRoot().Execute()`）。

- [ ] **Step 2: 修改 build.mk 构建路径**

`scripts/build/build.mk` 中 `$(PLATFORMS)` target 的构建命令，`./cmd` 改为 `./cmd/roverd`：

```makefile
	CGO_ENABLED=0 GOOS=$(os) GOARCH=$(ARCH) $(GO_BUILD) $(GO_BUILD_FLAGS) -ldflags "$(GO_BUILD_LDFLAGS)" -o $(OUT_DIR)/$(BINARY)-$(VERSION)-$(os)-$(ARCH) ./cmd/roverd
```

注意：`cmd/roverd.go` 原本有 `var version` 注入吗？检查 `-X main.version=$(VERSION)` 的注入点——若 `main` 包中无 `version` 变量则保持现状不动（ldflags 对不存在的变量无副作用）。

- [ ] **Step 3: 验证构建**

```bash
GOOS=linux go build ./cmd/roverd
make linux
ls bin/
```

预期：编译通过，产出 `skywalking-rover-*-linux-*`。

- [ ] **Step 4: Commit**

```bash
git add cmd scripts/build/build.mk
git commit -m "refactor: move roverd entrypoint to cmd/roverd"
```

---

### Task 3: 进程匹配逻辑 matcher（TDD）

**Files:**
- Create: `pkg/diagnosis/matcher/matcher.go`
- Test: `pkg/diagnosis/matcher/matcher_test.go`

- [ ] **Step 1: 编写失败测试**

`pkg/diagnosis/matcher/matcher_test.go`：

```go
package matcher

import (
	"testing"

	"github.com/shirou/gopsutil/process"

	processapi "github.com/apache/skywalking-rover/pkg/process/api"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
)

// fakeProcess 仅实现测试所需的最小行为
type fakeProcess struct {
	pid    int32
	entity *processapi.ProcessEntity
}

func (f *fakeProcess) ID() string                                { return "" }
func (f *fakeProcess) Pid() int32                                { return f.pid }
func (f *fakeProcess) DetectType() processapi.ProcessDetectType  { return processapi.Kubernetes }
func (f *fakeProcess) Entity() *processapi.ProcessEntity         { return f.entity }
func (f *fakeProcess) ProfilingStat() *profiling.Info            { return nil }
func (f *fakeProcess) ExeName() (string, error)                  { return f.entity.ProcessName, nil }
func (f *fakeProcess) OriginalProcess() *process.Process         { return nil }
func (f *fakeProcess) DetectProcess() processapi.DetectedProcess { return nil }
func (f *fakeProcess) PortIsExpose(int) bool                     { return false }
func (f *fakeProcess) DetectNewExposePort(int)                   {}
func (f *fakeProcess) ExposeHosts() []string                     { return nil }

func newFake(pid int32, service, instance, processName string) processapi.ProcessInterface {
	return &fakeProcess{pid: pid, entity: &processapi.ProcessEntity{
		ServiceName: service, InstanceName: instance, ProcessName: processName,
	}}
}

func TestMatchProcesses(t *testing.T) {
	processes := []processapi.ProcessInterface{
		newFake(1, "gateway.default", "gateway-0", "envoy"),
		newFake(2, "order.default", "order-0", "java"),
		newFake(3, "order.default", "order-1", "java"),
	}

	tests := []struct {
		name     string
		cond     *Condition
		wantPids []int32
	}{
		{name: "empty condition matches all", cond: &Condition{}, wantPids: []int32{1, 2, 3}},
		{name: "service substring", cond: &Condition{ServiceName: "order"}, wantPids: []int32{2, 3}},
		{name: "instance exact", cond: &Condition{InstanceName: "order-1"}, wantPids: []int32{3}},
		{name: "process name", cond: &Condition{ProcessName: "envoy"}, wantPids: []int32{1}},
		{name: "pid only", cond: &Condition{Pids: []int32{2}}, wantPids: []int32{2}},
		{name: "name and pid combined(AND)", cond: &Condition{ServiceName: "order", Pids: []int32{1, 2}}, wantPids: []int32{2}},
		{name: "no match", cond: &Condition{ServiceName: "not-exist"}, wantPids: []int32{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchProcesses(processes, tt.cond)
			gotPids := make([]int32, 0, len(got))
			for _, p := range got {
				gotPids = append(gotPids, p.Pid())
			}
			if len(gotPids) != len(tt.wantPids) {
				t.Fatalf("want pids %v, got %v", tt.wantPids, gotPids)
			}
			for i := range gotPids {
				if gotPids[i] != tt.wantPids[i] {
					t.Fatalf("want pids %v, got %v", tt.wantPids, gotPids)
				}
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./pkg/diagnosis/matcher/ -v
```

预期：编译失败，`Condition`、`MatchProcesses` 未定义。

- [ ] **Step 3: 实现 matcher**

`pkg/diagnosis/matcher/matcher.go`：

```go
package matcher

import (
	"strings"

	processapi "github.com/apache/skywalking-rover/pkg/process/api"
)

// Condition is the process filter condition, all non-empty fields are combined with AND
type Condition struct {
	ServiceName  string
	InstanceName string
	ProcessName  string
	Pids         []int32
}

// MatchProcesses filter the processes by the condition,
// the name conditions are substring matching
func MatchProcesses(processes []processapi.ProcessInterface, cond *Condition) []processapi.ProcessInterface {
	pidSet := make(map[int32]bool, len(cond.Pids))
	for _, pid := range cond.Pids {
		pidSet[pid] = true
	}
	result := make([]processapi.ProcessInterface, 0)
	for _, p := range processes {
		entity := p.Entity()
		if entity == nil {
			continue
		}
		if cond.ServiceName != "" && !strings.Contains(entity.ServiceName, cond.ServiceName) {
			continue
		}
		if cond.InstanceName != "" && !strings.Contains(entity.InstanceName, cond.InstanceName) {
			continue
		}
		if cond.ProcessName != "" && !strings.Contains(entity.ProcessName, cond.ProcessName) {
			continue
		}
		if len(pidSet) > 0 && !pidSet[p.Pid()] {
			continue
		}
		result = append(result, p)
	}
	return result
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./pkg/diagnosis/matcher/ -v
```

预期：全部 PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/diagnosis/matcher
git commit -m "feat(diagnosis): add process matching logic"
```

---

### Task 4: BPF 程序 trace connect

**Files:**
- Create: `bpf/diagnosis/trace/connect.c`
- Create: `pkg/diagnosis/trace/connect/runner.go`（本 Task 只放包声明与 go:generate，完整实现在 Task 6）
- Create（生成）: `pkg/diagnosis/trace/connect/bpf_*_bpfel.go` + `.o`

- [ ] **Step 1: 编写 BPF 程序**

`bpf/diagnosis/trace/connect.c`（带 Apache License 头；系统 include 集合参照 `bpf/profiling/network/netmonitor.c`，提供 `sockaddr_in/in6`、`bpf_ntohs`、`EINPROGRESS`）：

```c
// +build ignore

#include <linux/socket.h>
#include <linux/in.h>
#include <linux/in6.h>
#include <asm/errno.h>
#include <bpf/bpf_endian.h>
#include "api.h"

char __license[] SEC("license") = "Dual MIT/GPL";

// the pid white list, only the processes in this map would be traced
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32);
    __type(value, __u32);
} diagnosis_trace_connect_pids SEC(".maps");

struct diagnosis_trace_connect_args_t {
    __u64 start_nacs;
    struct sockaddr *addr;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 10000);
    __type(key, __u64);
    __type(value, struct diagnosis_trace_connect_args_t);
} diagnosis_trace_connect_args SEC(".maps");

struct {
    __uint(type, BPF_MAP_TYPE_PERF_EVENT_ARRAY);
    __uint(key_size, sizeof(__u32));
    __uint(value_size, sizeof(__u32));
} diagnosis_trace_connect_events SEC(".maps");

struct diagnosis_trace_connect_event_t {
    __u64 start_time;
    __u64 end_time;
    __u32 pid;
    __u32 family;
    __u32 success;
    __u32 error_code;
    __u32 remote_addr_v4;
    __u32 remote_port;
    __u8 remote_addr_v6[16];
};

SEC("tracepoint/syscalls/sys_enter_connect")
int diagnosis_trace_connect_enter(struct syscall_trace_enter *ctx) {
    __u64 id = bpf_get_current_pid_tgid();
    __u32 tgid = id >> 32;
    if (bpf_map_lookup_elem(&diagnosis_trace_connect_pids, &tgid) == NULL) {
        return 0;
    }

    struct diagnosis_trace_connect_args_t args = {};
    args.start_nacs = bpf_ktime_get_ns();
    args.addr = (struct sockaddr *)ctx->args[1];
    bpf_map_update_elem(&diagnosis_trace_connect_args, &id, &args, 0);
    return 0;
}

SEC("tracepoint/syscalls/sys_exit_connect")
int diagnosis_trace_connect_exit(struct syscall_trace_exit *ctx) {
    __u64 id = bpf_get_current_pid_tgid();
    struct diagnosis_trace_connect_args_t *args = bpf_map_lookup_elem(&diagnosis_trace_connect_args, &id);
    if (args == NULL) {
        return 0;
    }

    long ret = ctx->ret;
    struct sockaddr *addr = args->addr;
    __u16 family = 0;
    bpf_probe_read(&family, sizeof(family), &addr->sa_family);
    // only the IPv4/IPv6 connect would be reported
    if (family != AF_INET && family != AF_INET6) {
        goto cleanup;
    }

    struct diagnosis_trace_connect_event_t event = {};
    __u16 port = 0;
    if (family == AF_INET) {
        struct sockaddr_in *daddr = (struct sockaddr_in *)addr;
        bpf_probe_read(&event.remote_addr_v4, sizeof(event.remote_addr_v4), &daddr->sin_addr.s_addr);
        bpf_probe_read(&port, sizeof(port), &daddr->sin_port);
    } else {
        struct sockaddr_in6 *daddr = (struct sockaddr_in6 *)addr;
        bpf_probe_read(&event.remote_addr_v6, sizeof(event.remote_addr_v6), &daddr->sin6_addr.s6_addr);
        bpf_probe_read(&port, sizeof(port), &daddr->sin6_port);
    }
    event.remote_port = bpf_ntohs(port);
    event.start_time = args->start_nacs;
    event.end_time = bpf_ktime_get_ns();
    event.pid = id >> 32;
    event.family = family;
    event.success = (ret == 0 || ret == -EINPROGRESS) ? 1 : 0;
    event.error_code = event.success == 1 ? 0 : (__u32)(-ret);
    bpf_perf_event_output(ctx, &diagnosis_trace_connect_events, BPF_F_CURRENT_CPU, &event, sizeof(event));

cleanup:
    bpf_map_delete_elem(&diagnosis_trace_connect_args, &id);
    return 0;
}
```

注意：若 `api.h` 与系统头有重复定义冲突（如 AF_INET），`api.h` 内已有 `#ifndef` 保护；若编译报错按报错调整 include 顺序。

- [ ] **Step 2: 创建 Go 包并加 go:generate 指令**

`pkg/diagnosis/trace/connect/runner.go`（带 License 头，本步只含包声明与 generate 指令）：

```go
package connect

// $BPF_CLANG and $BPF_CFLAGS are set by the Makefile.
// nolint
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -no-global-types -target $TARGET -cc $BPF_CLANG -cflags $BPF_CFLAGS bpf $REPO_ROOT/bpf/diagnosis/trace/connect.c -- -I$REPO_ROOT/bpf/include
```

- [ ] **Step 3: 生成 BPF Go 绑定**

```bash
make container-generate
```

预期：`pkg/diagnosis/trace/connect/` 下生成 `bpf_*_bpfel.go` 与 `.o`，包含
`DiagnosisTraceConnectPids`、`DiagnosisTraceConnectArgs`、`DiagnosisTraceConnectEvents`
三个 map 与 `DiagnosisTraceConnectEnter`、`DiagnosisTraceConnectExit` 两个 program。

注意：`container-generate` 会重新生成全仓库 BPF 绑定，commit 时只添加本包新文件，其他包的无关变更用 `git checkout` 还原。

- [ ] **Step 4: 验证编译**

```bash
GOOS=linux go build ./pkg/diagnosis/...
```

- [ ] **Step 5: Commit**

```bash
git add bpf/diagnosis pkg/diagnosis/trace
git commit -m "feat(diagnosis): add trace connect BPF program with pid whitelist"
```

---

### Task 5: 事件结构与解析（TDD，复用 ip/host 工具）

**Files:**
- Create: `pkg/diagnosis/trace/connect/event.go`
- Test: `pkg/diagnosis/trace/connect/event_test.go`

- [ ] **Step 1: 编写失败测试**

`pkg/diagnosis/trace/connect/event_test.go`：

```go
package connect

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestEventRemoteAddress(t *testing.T) {
	// IPv4: 10.96.0.10:8080，remote_addr_v4 为网络序原始字节（ip.ParseIPV4 约定）
	v4 := &Event{Family: afInet, RemotePort: 8080}
	v4.RemoteAddrV4 = binary.LittleEndian.Uint32([]byte{10, 96, 0, 10})
	if got := v4.RemoteAddress(); got != "10.96.0.10:8080" {
		t.Fatalf("want 10.96.0.10:8080, got %s", got)
	}

	// IPv6: [::1]:443
	v6 := &Event{Family: afInet6, RemotePort: 443}
	v6.RemoteAddrV6[15] = 1
	if got := v6.RemoteAddress(); got != "[::1]:443" {
		t.Fatalf("want [::1]:443, got %s", got)
	}

	// unknown family
	unknown := &Event{Family: 1}
	if got := unknown.RemoteAddress(); got != "unknown" {
		t.Fatalf("want unknown, got %s", got)
	}
}

func TestEventLatency(t *testing.T) {
	e := &Event{StartTime: 1000, EndTime: 301000}
	if got := e.Latency(); got != 300*time.Microsecond {
		t.Fatalf("want 300µs, got %s", got)
	}
}
```

注意：`ip.ParseIPV4(uint32)` 的字节序约定以 `pkg/tools/ip/bpf.go` 实际实现为准——
若测试中 `binary.LittleEndian` 断言失败，查看 `ParseIPV4` 源码后改用对应字节序构造测试值（实现不改，改测试构造方式）。

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./pkg/diagnosis/trace/connect/ -v
```

预期：编译失败，`Event`、`afInet` 未定义。（macOS 因生成绑定无法编译时，用 `GOOS=linux go vet` 校验、linux 环境跑测试，下同。）

- [ ] **Step 3: 实现 Event（复用 btf.Reader / ip.Parse / host.Time）**

`pkg/diagnosis/trace/connect/event.go`：

```go
package connect

import (
	"fmt"
	"time"

	"github.com/apache/skywalking-rover/pkg/tools/btf"
	"github.com/apache/skywalking-rover/pkg/tools/host"
	"github.com/apache/skywalking-rover/pkg/tools/ip"
)

const (
	afInet  = 2
	afInet6 = 10
)

// Event is the perf event reported from the BPF program,
// the layout MUST be same with struct diagnosis_trace_connect_event_t in connect.c
type Event struct {
	StartTime    uint64
	EndTime      uint64
	PID          uint32
	Family       uint32
	Success      uint32
	ErrorCode    uint32
	RemoteAddrV4 uint32
	RemotePort   uint32
	RemoteAddrV6 [16]uint8
}

// ReadFrom implement the btf.EventReader interface
func (e *Event) ReadFrom(r btf.Reader) {
	e.StartTime = r.ReadUint64()
	e.EndTime = r.ReadUint64()
	e.PID = r.ReadUint32()
	e.Family = r.ReadUint32()
	e.Success = r.ReadUint32()
	e.ErrorCode = r.ReadUint32()
	e.RemoteAddrV4 = r.ReadUint32()
	e.RemotePort = r.ReadUint32()
	r.ReadUint8Array(e.RemoteAddrV6[:], 16)
}

// RemoteAddress build the remote address string
func (e *Event) RemoteAddress() string {
	switch e.Family {
	case afInet:
		return fmt.Sprintf("%s:%d", ip.ParseIPV4(e.RemoteAddrV4), e.RemotePort)
	case afInet6:
		return fmt.Sprintf("[%s]:%d", ip.ParseIPV6(e.RemoteAddrV6), e.RemotePort)
	}
	return "unknown"
}

// Timestamp convert the BPF ktime to the real wall-clock time
func (e *Event) Timestamp() time.Time {
	return host.Time(e.EndTime)
}

// Latency is the duration of the connect syscall
func (e *Event) Latency() time.Duration {
	return time.Duration(e.EndTime - e.StartTime)
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./pkg/diagnosis/trace/connect/ -v
```

预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/diagnosis/trace/connect/event.go pkg/diagnosis/trace/connect/event_test.go
git commit -m "feat(diagnosis): add trace connect event parsing with reused ip/host tools"
```

---

### Task 6: trace connect Runner（BPF 加载/卸载/事件通道）

**Files:**
- Modify: `pkg/diagnosis/trace/connect/runner.go`

- [ ] **Step 1: 实现 Runner**

在 Task 4 创建的 `runner.go` 中补全（保留 go:generate 行）：

```go
package connect

import (
	"fmt"

	"github.com/apache/skywalking-rover/pkg/logger"
	"github.com/apache/skywalking-rover/pkg/tools/btf"
)

// $BPF_CLANG and $BPF_CFLAGS are set by the Makefile.
// nolint
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -no-global-types -target $TARGET -cc $BPF_CLANG -cflags $BPF_CFLAGS bpf $REPO_ROOT/bpf/diagnosis/trace/connect.c -- -I$REPO_ROOT/bpf/include

var log = logger.GetLogger("diagnosis", "trace", "connect")

// Runner load the trace connect BPF program and trace the specified pids,
// each Runner instance owns its dedicated BPF objects,
// so the concurrent tracing sessions would not affect each other
type Runner struct {
	objs   *bpfObjects
	linker *btf.Linker
	events chan *Event
}

func NewRunner() *Runner {
	return &Runner{events: make(chan *Event, 1000)}
}

// Start load the BPF program and return the event channel,
// the channel would be closed after Stop
func (r *Runner) Start(pids []int32) (<-chan *Event, error) {
	objs := bpfObjects{}
	if err := btf.LoadBPFAndAssign(loadBpf, &objs); err != nil {
		return nil, fmt.Errorf("load trace connect bpf failure: %v", err)
	}
	r.objs = &objs

	traced := uint32(1)
	for _, pid := range pids {
		if err := objs.DiagnosisTraceConnectPids.Put(uint32(pid), traced); err != nil {
			_ = objs.Close()
			return nil, fmt.Errorf("update the trace pid(%d) failure: %v", pid, err)
		}
	}

	linker := btf.NewLinker()
	linker.AddTracePoint("syscalls", "sys_enter_connect", objs.DiagnosisTraceConnectEnter)
	linker.AddTracePoint("syscalls", "sys_exit_connect", objs.DiagnosisTraceConnectExit)
	linker.ReadEventAsync(objs.DiagnosisTraceConnectEvents, r.handleEvent, func() interface{} {
		return &Event{}
	})
	if err := linker.HasError(); err != nil {
		_ = linker.Close()
		_ = objs.Close()
		return nil, fmt.Errorf("link trace connect bpf failure: %v", err)
	}
	r.linker = linker
	return r.events, nil
}

func (r *Runner) handleEvent(data interface{}) {
	event, ok := data.(*Event)
	if !ok {
		return
	}
	select {
	case r.events <- event:
	default:
		log.Warnf("the trace connect event queue is full, dropping event from pid: %d", event.PID)
	}
}

// Stop unload the BPF program and close the event channel
func (r *Runner) Stop() error {
	var err error
	if r.linker != nil {
		err = r.linker.Close()
		r.linker = nil
	}
	if r.objs != nil {
		if closeErr := r.objs.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		r.objs = nil
	}
	close(r.events)
	return err
}
```

- [ ] **Step 2: 验证编译与已有测试**

```bash
GOOS=linux go build ./pkg/diagnosis/...
go test ./pkg/diagnosis/trace/connect/ -v
```

预期：编译通过，Task 5 的测试仍 PASS。

- [ ] **Step 3: Commit**

```bash
git add pkg/diagnosis/trace/connect/runner.go
git commit -m "feat(diagnosis): add trace connect runner"
```

---

### Task 7: DiagnosisService server — ListProcesses（TDD）

**Files:**
- Create: `pkg/diagnosis/server.go`
- Test: `pkg/diagnosis/server_test.go`

- [ ] **Step 1: 编写失败测试**

`pkg/diagnosis/server_test.go`（fakeProcess 与 Task 3 相同结构，本包内重复定义，
另增 `id` 字段用于验证 reported_to_backend）：

```go
package diagnosis

import (
	"context"
	"testing"

	"github.com/shirou/gopsutil/process"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
	processapi "github.com/apache/skywalking-rover/pkg/process/api"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
)

type fakeProcess struct {
	pid    int32
	id     string
	entity *processapi.ProcessEntity
}

func (f *fakeProcess) ID() string                                { return f.id }
func (f *fakeProcess) Pid() int32                                { return f.pid }
func (f *fakeProcess) DetectType() processapi.ProcessDetectType  { return processapi.Kubernetes }
func (f *fakeProcess) Entity() *processapi.ProcessEntity         { return f.entity }
func (f *fakeProcess) ProfilingStat() *profiling.Info            { return nil }
func (f *fakeProcess) ExeName() (string, error)                  { return f.entity.ProcessName, nil }
func (f *fakeProcess) OriginalProcess() *process.Process         { return nil }
func (f *fakeProcess) DetectProcess() processapi.DetectedProcess { return nil }
func (f *fakeProcess) PortIsExpose(int) bool                     { return false }
func (f *fakeProcess) DetectNewExposePort(int)                   {}
func (f *fakeProcess) ExposeHosts() []string                     { return nil }

type fakeLister struct {
	processes []processapi.ProcessInterface
}

func (f *fakeLister) GetAllProcesses() []processapi.ProcessInterface {
	return f.processes
}

func TestListProcesses(t *testing.T) {
	lister := &fakeLister{processes: []processapi.ProcessInterface{
		// 已上报后端的进程
		&fakeProcess{pid: 1, id: "id-1", entity: &processapi.ProcessEntity{
			Layer: "MESH_DP", ServiceName: "gateway.default", InstanceName: "gateway-0",
			ProcessName: "envoy", Labels: []string{"mesh-envoy"},
		}},
		// 本地已发现但尚未上报后端的进程（id 为空）
		&fakeProcess{pid: 2, id: "", entity: &processapi.ProcessEntity{
			Layer: "K8S_SERVICE", ServiceName: "order.default", InstanceName: "order-0",
			ProcessName: "java", Labels: []string{"k8s-service"},
		}},
	}}
	server := newServer(lister)

	// 不要求已上报后端也要可见
	resp, err := server.ListProcesses(context.Background(), &diagnosisv1.ListProcessesRequest{
		Selector: &diagnosisv1.ProcessSelector{ServiceName: "order"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Processes) != 1 {
		t.Fatalf("want 1 process, got %d", len(resp.Processes))
	}
	got := resp.Processes[0]
	if got.Pid != 2 || got.EntityId != "" || got.ReportedToBackend {
		t.Fatalf("unexpected not-reported process info: %+v", got)
	}

	// 已上报的进程 reported_to_backend 应为 true
	resp, err = server.ListProcesses(context.Background(), &diagnosisv1.ListProcessesRequest{
		Selector: &diagnosisv1.ProcessSelector{ServiceName: "gateway"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Processes) != 1 || !resp.Processes[0].ReportedToBackend || resp.Processes[0].EntityId != "id-1" {
		t.Fatalf("unexpected reported process info: %+v", resp.Processes[0])
	}

	// selector 为空时返回全部
	resp, err = server.ListProcesses(context.Background(), &diagnosisv1.ListProcessesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Processes) != 2 {
		t.Fatalf("want 2 processes, got %d", len(resp.Processes))
	}
}
```

- [ ] **Step 2: 校验确认失败**

```bash
GOOS=linux go vet ./pkg/diagnosis/ 2>&1 | head -5
```

预期：`newServer` 未定义。

- [ ] **Step 3: 实现 server（本 Task 仅 ListProcesses）**

`pkg/diagnosis/server.go`：

```go
package diagnosis

import (
	"context"

	"github.com/apache/skywalking-rover/pkg/diagnosis/matcher"
	"github.com/apache/skywalking-rover/pkg/logger"
	processapi "github.com/apache/skywalking-rover/pkg/process/api"
	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

var log = logger.GetLogger("diagnosis")

// processLister abstract the process module for testing
type processLister interface {
	GetAllProcesses() []processapi.ProcessInterface
}

type server struct {
	diagnosisv1.UnimplementedDiagnosisServiceServer

	processLister processLister
}

func newServer(lister processLister) *server {
	return &server{processLister: lister}
}

// selectorToCondition convert the proto selector(nullable) to the matcher condition
func selectorToCondition(selector *diagnosisv1.ProcessSelector) *matcher.Condition {
	if selector == nil {
		return &matcher.Condition{}
	}
	return &matcher.Condition{
		ServiceName:  selector.ServiceName,
		InstanceName: selector.InstanceName,
		ProcessName:  selector.ProcessName,
		Pids:         selector.Pids,
	}
}

func (s *server) ListProcesses(_ context.Context, req *diagnosisv1.ListProcessesRequest) (*diagnosisv1.ListProcessesResponse, error) {
	matched := matcher.MatchProcesses(s.processLister.GetAllProcesses(), selectorToCondition(req.Selector))
	infos := make([]*diagnosisv1.ProcessInfo, 0, len(matched))
	for _, p := range matched {
		infos = append(infos, buildProcessInfo(p))
	}
	return &diagnosisv1.ListProcessesResponse{Processes: infos}, nil
}

func buildProcessInfo(p processapi.ProcessInterface) *diagnosisv1.ProcessInfo {
	entity := p.Entity()
	info := &diagnosisv1.ProcessInfo{
		Pid:               p.Pid(),
		EntityId:          p.ID(),
		Layer:             entity.Layer,
		ServiceName:       entity.ServiceName,
		InstanceName:      entity.InstanceName,
		ProcessName:       entity.ProcessName,
		Labels:            entity.Labels,
		ReportedToBackend: p.ID() != "",
	}
	if exe, err := p.ExeName(); err == nil {
		info.ExePath = exe
	}
	if original := p.OriginalProcess(); original != nil {
		if cmdline, err := original.Cmdline(); err == nil {
			info.CommandLine = cmdline
		}
	}
	return info
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./pkg/diagnosis/ -v -run TestListProcesses
```

（macOS 编译不过则 linux 环境执行。）预期：PASS。

- [ ] **Step 5: Commit**

```bash
git add pkg/diagnosis/server.go pkg/diagnosis/server_test.go
git commit -m "feat(diagnosis): implement ListProcesses with backend report status"
```

---

### Task 8: TraceConnect RPC 实现

**Files:**
- Modify: `pkg/diagnosis/server.go`

- [ ] **Step 1: 实现 TraceConnect**

在 `pkg/diagnosis/server.go` 中追加（import 增加
`"google.golang.org/grpc/codes"`、`"google.golang.org/grpc/status"`、
`traceconnect "github.com/apache/skywalking-rover/pkg/diagnosis/trace/connect"`）：

```go
func (s *server) TraceConnect(req *diagnosisv1.TraceConnectRequest, stream diagnosisv1.DiagnosisService_TraceConnectServer) error {
	matched := matcher.MatchProcesses(s.processLister.GetAllProcesses(), selectorToCondition(req.Selector))
	if len(matched) == 0 {
		return status.Error(codes.NotFound, "no process matched with the request selector")
	}

	pids := make([]int32, 0, len(matched))
	processNames := make(map[uint32]string, len(matched))
	for _, p := range matched {
		pids = append(pids, p.Pid())
		processNames[uint32(p.Pid())] = p.Entity().ProcessName
	}

	runner := traceconnect.NewRunner()
	events, err := runner.Start(pids)
	if err != nil {
		return status.Errorf(codes.Internal, "start trace connect failure: %v", err)
	}
	defer func() {
		if stopErr := runner.Stop(); stopErr != nil {
			log.Warnf("stop trace connect failure: %v", stopErr)
		}
	}()
	log.Infof("trace connect started, pids: %v", pids)

	for {
		select {
		case <-stream.Context().Done():
			log.Infof("trace connect stopped, pids: %v", pids)
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			sendErr := stream.Send(&diagnosisv1.ConnectEvent{
				TimestampNs:   event.Timestamp().UnixNano(),
				Pid:           int32(event.PID),
				ProcessName:   processNames[event.PID],
				RemoteAddress: event.RemoteAddress(),
				Success:       event.Success == 1,
				Errno:         int32(event.ErrorCode),
				LatencyNs:     int64(event.Latency()),
			})
			if sendErr != nil {
				return sendErr
			}
		}
	}
}
```

- [ ] **Step 2: 验证编译与已有测试**

```bash
GOOS=linux go build ./pkg/diagnosis/...
go test ./pkg/diagnosis/... -v
```

预期：编译通过，已有测试 PASS。

- [ ] **Step 3: Commit**

```bash
git add pkg/diagnosis/server.go
git commit -m "feat(diagnosis): implement TraceConnect streaming handler"
```

---

### Task 9: diagnosis 模块接入 daemon

**Files:**
- Create: `pkg/diagnosis/config.go`
- Create: `pkg/diagnosis/module.go`
- Modify: `pkg/boot/register.go`
- Modify: `configs/rover_configs.yaml`

- [ ] **Step 1: 编写 Config**

`pkg/diagnosis/config.go`：

```go
package diagnosis

import "github.com/apache/skywalking-rover/pkg/module"

type Config struct {
	module.Config `mapstructure:",squash"`

	// Host is the bind host of the diagnosis gRPC server
	Host string `mapstructure:"host"`
	// Port is the bind port of the diagnosis gRPC server
	Port int `mapstructure:"port"`
}

func (c *Config) IsActive() bool {
	return c.Active
}
```

- [ ] **Step 2: 编写 Module**

`pkg/diagnosis/module.go`：

```go
package diagnosis

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	"github.com/apache/skywalking-rover/pkg/module"
	"github.com/apache/skywalking-rover/pkg/process"
	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

const ModuleName = "diagnosis"

type Module struct {
	config *Config

	grpcServer *grpc.Server
	shutdown   bool
}

func NewModule() *Module {
	return &Module{config: &Config{}}
}

func (m *Module) Name() string {
	return ModuleName
}

func (m *Module) RequiredModules() []string {
	return []string{process.ModuleName}
}

func (m *Module) Config() module.ConfigInterface {
	return m.config
}

func (m *Module) Start(_ context.Context, mgr *module.Manager) error {
	processModule := mgr.FindModule(process.ModuleName).(*process.Module)
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", m.config.Host, m.config.Port))
	if err != nil {
		return fmt.Errorf("the diagnosis server listen failure: %v", err)
	}
	m.grpcServer = grpc.NewServer()
	diagnosisv1.RegisterDiagnosisServiceServer(m.grpcServer, newServer(processModule))
	go func() {
		m.shutdown = false
		if serveErr := m.grpcServer.Serve(listener); serveErr != nil && !m.shutdown {
			mgr.ShutdownModules(serveErr)
		}
	}()
	log.Infof("the diagnosis server is listening on %s:%d", m.config.Host, m.config.Port)
	return nil
}

func (m *Module) NotifyStartSuccess() {
}

func (m *Module) Shutdown(context.Context, *module.Manager) error {
	m.shutdown = true
	if m.grpcServer != nil {
		// Stop would cancel all in-flight tracing streams,
		// then the deferred runner.Stop unload the BPF programs
		m.grpcServer.Stop()
	}
	return nil
}
```

- [ ] **Step 3: 注册模块**

`pkg/boot/register.go` 的 `init()` 中追加（import 加 `"github.com/apache/skywalking-rover/pkg/diagnosis"`）：

```go
	module.Register(diagnosis.NewModule())
```

- [ ] **Step 4: 新增配置段**

`configs/rover_configs.yaml` 在 `pprof:` 段之前插入：

```yaml
diagnosis:
  # Is active the diagnosis gRPC server for the rover-cli
  active: ${ROVER_DIAGNOSIS_ACTIVE:true}
  # The bind host of the diagnosis gRPC server
  host: ${ROVER_DIAGNOSIS_HOST:127.0.0.1}
  # The bind port of the diagnosis gRPC server
  port: ${ROVER_DIAGNOSIS_PORT:12700}
```

- [ ] **Step 5: 验证编译**

```bash
GOOS=linux go build ./...
```

预期：编译通过。

- [ ] **Step 6: Commit**

```bash
git add pkg/diagnosis/config.go pkg/diagnosis/module.go pkg/boot/register.go configs/rover_configs.yaml
git commit -m "feat(diagnosis): register diagnosis module with gRPC server"
```

---

### Task 10: rover-cli 骨架与 process list 命令（输出格式化 TDD）

**Files:**
- Create: `cmd/cli/main.go`
- Create: `cmd/cli/root.go`
- Create: `cmd/cli/process.go`
- Create: `cmd/cli/output.go`
- Test: `cmd/cli/output_test.go`

- [ ] **Step 1: 编写输出格式化的失败测试**

`cmd/cli/output_test.go`：

```go
package main

import (
	"bytes"
	"strings"
	"testing"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func TestPrintProcessesTable(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{
		{Pid: 1234, Layer: "MESH_DP", ServiceName: "gateway.default",
			InstanceName: "gateway-0", ProcessName: "envoy",
			Labels: []string{"mesh-envoy"}, ReportedToBackend: true},
	}
	if err := printProcesses(&buf, processes, "table"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PID", "SERVICE", "REPORTED", "1234", "gateway.default", "envoy", "mesh-envoy", "true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q, got:\n%s", want, out)
		}
	}
}

func TestPrintProcessesJSON(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{{Pid: 1234, ServiceName: "gateway.default"}}
	if err := printProcesses(&buf, processes, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"pid":1234`) || !strings.Contains(out, `"serviceName":"gateway.default"`) {
		t.Fatalf("unexpected json output:\n%s", out)
	}
}

func TestPrintProcessesYAML(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{{Pid: 1234, ServiceName: "gateway.default"}}
	if err := printProcesses(&buf, processes, "yaml"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "pid: 1234") || !strings.Contains(out, "serviceName: gateway.default") {
		t.Fatalf("unexpected yaml output:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./cmd/cli/ -v
```

预期：编译失败，`printProcesses` 未定义。

- [ ] **Step 3: 实现 output.go**

`cmd/cli/output.go`：

```go
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/yaml"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func protoToJSON(m proto.Message) ([]byte, error) {
	return protojson.MarshalOptions{}.Marshal(m)
}

func protoToYAML(m proto.Message) ([]byte, error) {
	jsonData, err := protoToJSON(m)
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(jsonData)
}

func printProcesses(w io.Writer, processes []*diagnosisv1.ProcessInfo, format string) error {
	switch format {
	case "json":
		for _, p := range processes {
			data, err := protoToJSON(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(w, string(data))
		}
		return nil
	case "yaml":
		// output as a single YAML array for the e2e verification
		items := make([]json.RawMessage, 0, len(processes))
		for _, p := range processes {
			data, err := protoToJSON(p)
			if err != nil {
				return err
			}
			items = append(items, data)
		}
		jsonData, err := json.Marshal(items)
		if err != nil {
			return err
		}
		yamlData, err := yaml.JSONToYAML(jsonData)
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(yamlData))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PID\tLAYER\tSERVICE\tINSTANCE\tPROCESS\tLABELS\tREPORTED")
	for _, p := range processes {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%t\n",
			p.Pid, p.Layer, p.ServiceName, p.InstanceName, p.ProcessName,
			strings.Join(p.Labels, ","), p.ReportedToBackend)
	}
	return tw.Flush()
}
```

- [ ] **Step 4: 实现 main.go / root.go / process.go**

`cmd/cli/main.go`：

```go
package main

import "os"

func main() {
	if err := newRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
```

`cmd/cli/root.go`：

```go
package main

import (
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var serverAddr string

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "rover-cli",
		Short:        "rover-cli is a real-time diagnosis tool for the skywalking-rover",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&serverAddr, "addr", "127.0.0.1:12700",
		"the address of the rover diagnosis server")
	cmd.AddCommand(newProcessCmd())
	cmd.AddCommand(newTraceCmd())
	return cmd
}

func dialServer() (*grpc.ClientConn, error) {
	return grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}
```

`cmd/cli/process.go`：

```go
package main

import (
	"context"
	"os"
	"time"

	"github.com/spf13/cobra"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "explore the processes discovered by the rover",
	}
	cmd.AddCommand(newProcessListCmd())
	return cmd
}

func newProcessListCmd() *cobra.Command {
	var service, instance, processName, format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the discovered processes",
		RunE: func(*cobra.Command, []string) error {
			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			resp, err := diagnosisv1.NewDiagnosisServiceClient(conn).ListProcesses(ctx, &diagnosisv1.ListProcessesRequest{
				Selector: &diagnosisv1.ProcessSelector{
					ServiceName:  service,
					InstanceName: instance,
					ProcessName:  processName,
				},
			})
			if err != nil {
				return err
			}
			return printProcesses(os.Stdout, resp.Processes, format)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "filter by the service name(substring matching)")
	cmd.Flags().StringVar(&instance, "instance", "", "filter by the instance name(substring matching)")
	cmd.Flags().StringVar(&processName, "process", "", "filter by the process name(substring matching)")
	cmd.Flags().StringVar(&format, "format", "table", `the output format, support "table", "json" and "yaml"`)
	return cmd
}
```

`cmd/cli/trace.go` 暂时占位（Task 11 实现，先保证编译）：

```go
package main

import "github.com/spf13/cobra"

func newTraceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trace",
		Short: "real-time tracing on the target processes",
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
go test ./cmd/cli/ -v
go build ./cmd/cli
go mod tidy   # sigs.k8s.io/yaml 转为 direct 依赖
```

预期：测试 PASS，编译通过（CLI 无 BPF 依赖，macOS 可直接构建）。

- [ ] **Step 6: Commit**

```bash
git add cmd/cli go.mod go.sum
git commit -m "feat(cli): add rover-cli skeleton with process list command"
```

---

### Task 11: trace connect 命令与统计聚合（TDD）

**Files:**
- Create: `cmd/cli/stats.go`
- Test: `cmd/cli/stats_test.go`
- Modify: `cmd/cli/trace.go`
- Modify: `cmd/cli/output.go`
- Modify: `cmd/cli/output_test.go`

- [ ] **Step 1: 编写统计聚合的失败测试**

`cmd/cli/stats_test.go`：

```go
package main

import "testing"

func TestStatsCollector(t *testing.T) {
	collector := newStatsCollector()
	collector.Add("10.96.0.10:9090", true)
	collector.Add("10.96.0.10:9090", true)
	collector.Add("10.96.0.11:443", false)

	rows := collector.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	first := rows[0]
	if first.Remote != "10.96.0.10:9090" || first.Total != 2 || first.Success != 2 ||
		first.Failed != 0 || first.FailPercent != 0 {
		t.Fatalf("unexpected first row: %+v", first)
	}
	second := rows[1]
	if second.Remote != "10.96.0.11:443" || second.Total != 1 || second.Failed != 1 ||
		second.FailPercent != 100 {
		t.Fatalf("unexpected second row: %+v", second)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./cmd/cli/ -v -run TestStatsCollector
```

预期：编译失败，`newStatsCollector` 未定义。

- [ ] **Step 3: 实现 stats.go**

`cmd/cli/stats.go`：

```go
package main

type statsRow struct {
	Remote      string  `json:"remote"`
	Total       int     `json:"total"`
	Success     int     `json:"success"`
	Failed      int     `json:"failed"`
	FailPercent float64 `json:"failPercent"`
}

type statsCollector struct {
	order []string
	rows  map[string]*statsRow
}

func newStatsCollector() *statsCollector {
	return &statsCollector{rows: make(map[string]*statsRow)}
}

func (s *statsCollector) Add(remote string, success bool) {
	row := s.rows[remote]
	if row == nil {
		row = &statsRow{Remote: remote}
		s.rows[remote] = row
		s.order = append(s.order, remote)
	}
	row.Total++
	if success {
		row.Success++
	} else {
		row.Failed++
	}
	row.FailPercent = float64(row.Failed) / float64(row.Total) * 100
}

// Rows return the statistics rows ordered by the first appearance
func (s *statsCollector) Rows() []*statsRow {
	result := make([]*statsRow, 0, len(s.order))
	for _, remote := range s.order {
		result = append(result, s.rows[remote])
	}
	return result
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./cmd/cli/ -v -run TestStatsCollector
```

预期：PASS。

- [ ] **Step 5: output.go 追加事件与统计输出**

`cmd/cli/output.go` 追加（import 增加 `"time"`）：

```go
func printEventHeader(w io.Writer) {
	fmt.Fprintf(w, "%-15s %-8s %-15s %-25s %-18s %s\n",
		"TIME", "PID", "PROCESS", "REMOTE", "RESULT", "LATENCY")
}

func formatEventText(e *diagnosisv1.ConnectEvent) string {
	result := "SUCCESS"
	if !e.Success {
		result = fmt.Sprintf("FAILED(errno=%d)", e.Errno)
	}
	return fmt.Sprintf("%-15s %-8d %-15s %-25s %-18s %s",
		time.Unix(0, e.TimestampNs).Format("15:04:05.000"),
		e.Pid, e.ProcessName, e.RemoteAddress, result, time.Duration(e.LatencyNs))
}

func printEvent(w io.Writer, e *diagnosisv1.ConnectEvent, format string) error {
	switch format {
	case "json":
		data, err := protoToJSON(e)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
	case "yaml":
		data, err := protoToYAML(e)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "---\n%s", string(data))
	default:
		fmt.Fprintln(w, formatEventText(e))
	}
	return nil
}

func printStats(w io.Writer, collector *statsCollector, format string) error {
	rows := collector.Rows()
	switch format {
	case "json":
		data, err := json.Marshal(map[string]interface{}{"statistics": rows})
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	case "yaml":
		jsonData, err := json.Marshal(map[string]interface{}{"statistics": rows})
		if err != nil {
			return err
		}
		yamlData, err := yaml.JSONToYAML(jsonData)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "---\n%s", string(yamlData))
		return nil
	}
	fmt.Fprintln(w, "\n--- tcp connect statistics ---")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REMOTE\tTOTAL\tSUCCESS\tFAILED\tFAIL%")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.0f%%\n",
			row.Remote, row.Total, row.Success, row.Failed, row.FailPercent)
	}
	return tw.Flush()
}
```

`cmd/cli/output_test.go` 追加：

```go
func TestFormatEventText(t *testing.T) {
	event := &diagnosisv1.ConnectEvent{
		TimestampNs: 0, Pid: 1234, ProcessName: "envoy",
		RemoteAddress: "10.96.0.11:443", Success: false, Errno: 111, LatencyNs: 300000,
	}
	out := formatEventText(event)
	for _, want := range []string{"1234", "envoy", "10.96.0.11:443", "FAILED(errno=111)", "300µs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q, got: %s", want, out)
		}
	}
}
```

- [ ] **Step 6: 实现 trace connect 命令**

`cmd/cli/trace.go` 完整替换为：

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func newTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "real-time tracing on the target processes",
	}
	cmd.AddCommand(newTraceConnectCmd())
	return cmd
}

func newTraceConnectCmd() *cobra.Command {
	var service, instance, processName, format string
	var pids []int32
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "trace the TCP connect operations of the target processes",
		RunE: func(*cobra.Command, []string) error {
			if service == "" && instance == "" && processName == "" && len(pids) == 0 {
				return fmt.Errorf("at least one of --service, --instance, --process, --pid is required")
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if duration > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, duration)
				defer cancel()
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()
			stream, err := diagnosisv1.NewDiagnosisServiceClient(conn).TraceConnect(ctx, &diagnosisv1.TraceConnectRequest{
				Selector: &diagnosisv1.ProcessSelector{
					ServiceName:  service,
					InstanceName: instance,
					ProcessName:  processName,
					Pids:         pids,
				},
			})
			if err != nil {
				return err
			}

			collector := newStatsCollector()
			if format == "text" {
				printEventHeader(os.Stdout)
			}
			for {
				event, recvErr := stream.Recv()
				if recvErr != nil {
					if statsErr := printStats(os.Stdout, collector, format); statsErr != nil {
						return statsErr
					}
					if isExpectedEnd(recvErr) {
						return nil
					}
					return recvErr
				}
				collector.Add(event.RemoteAddress, event.Success)
				if printErr := printEvent(os.Stdout, event, format); printErr != nil {
					return printErr
				}
			}
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "trace the processes matched with the service name(substring matching)")
	cmd.Flags().StringVar(&instance, "instance", "", "trace the processes matched with the instance name(substring matching)")
	cmd.Flags().StringVar(&processName, "process", "", "trace the processes matched with the process name(substring matching)")
	cmd.Flags().Int32SliceVar(&pids, "pid", nil, "trace the specified pids")
	cmd.Flags().DurationVar(&duration, "duration", 0, "the tracing duration, trace until interrupted if not set")
	cmd.Flags().StringVar(&format, "format", "text", `the output format, support "text", "json" and "yaml"`)
	return cmd
}

// isExpectedEnd check the stream end is triggered by the user interrupt or duration reached
func isExpectedEnd(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	s, ok := status.FromError(err)
	if !ok {
		return false
	}
	return s.Code() == codes.Canceled || s.Code() == codes.DeadlineExceeded
}
```

- [ ] **Step 7: 运行全部 CLI 测试与编译**

```bash
go test ./cmd/cli/ -v
go build ./cmd/cli
```

预期：全部 PASS，编译通过。

- [ ] **Step 8: Commit**

```bash
git add cmd/cli
git commit -m "feat(cli): add trace connect command with streaming output and statistics"
```

---

### Task 12: 构建脚本与镜像集成

**Files:**
- Modify: `scripts/build/build.mk`
- Modify: `scripts/build/test.mk`、`scripts/build/lint.mk`
- Modify: `docker/Dockerfile.build`

- [ ] **Step 1: build.mk 增加 rover-cli 编译与 proto-gen 前置**

`scripts/build/build.mk`：`BINARY = skywalking-rover` 下方加：

```makefile
CLI_BINARY = rover-cli
```

`$(PLATFORMS)` target 改为依赖 `deps proto-gen`，并追加 CLI 构建行：

```makefile
$(PLATFORMS): deps proto-gen
	mkdir -p $(OUT_DIR)
	CGO_ENABLED=0 GOOS=$(os) GOARCH=$(ARCH) $(GO_BUILD) $(GO_BUILD_FLAGS) -ldflags "$(GO_BUILD_LDFLAGS)" -o $(OUT_DIR)/$(BINARY)-$(VERSION)-$(os)-$(ARCH) ./cmd/roverd
	CGO_ENABLED=0 GOOS=$(os) GOARCH=$(ARCH) $(GO_BUILD) $(GO_BUILD_FLAGS) -ldflags "$(GO_BUILD_LDFLAGS)" -o $(OUT_DIR)/$(CLI_BINARY)-$(VERSION)-$(os)-$(ARCH) ./cmd/cli
```

- [ ] **Step 2: test/lint target 增加 proto-gen 前置**

在 `scripts/build/test.mk` 的 `test:` target 与 `scripts/build/lint.mk` 的 `lint:` target 上增加 `proto-gen` 依赖（以两个文件中实际的 target 名为准，保持其原有依赖不变、追加 proto-gen）。

- [ ] **Step 3: Dockerfile.build 加入 rover-cli**

`docker/Dockerfile.build`：

`RUN mv ...` 行改为：

```dockerfile
RUN mv /src/bin/skywalking-rover-${VERSION}-linux-* /src/bin/skywalking-rover && \
    mv /src/bin/rover-cli-${VERSION}-linux-* /src/bin/rover-cli
```

`COPY --from=build /src/bin/skywalking-rover /` 之后追加：

```dockerfile
COPY --from=build /src/bin/rover-cli /
```

- [ ] **Step 4: 验证构建**

```bash
make linux
ls bin/ | grep -E 'rover-cli|skywalking-rover'
```

预期：`bin/` 下出现 `skywalking-rover-*` 与 `rover-cli-*` 两个产物。

- [ ] **Step 5: Commit**

```bash
git add scripts/build docker/Dockerfile.build
git commit -m "build: package rover-cli into the rover image"
```

---

### Task 13: 配置文档

**Files:**
- Create: `docs/en/setup/configuration/diagnosis.md`

- [ ] **Step 1: 先查看 `docs/en/setup/configuration/` 下已有文档的结构与风格**（标题层级、配置表格列名），新文档保持一致。内容要点：

```markdown
# Diagnosis Module

The diagnosis module provides a gRPC server for the `rover-cli` to
explore the discovered processes and execute the real-time diagnosis tasks,
such as tracing the TCP connect operations of the target processes.

The server only binds the localhost by default. If you want to access it
from the remote machine, please update the host configuration.

| Name | Default | Environment Key | Description |
|------|---------|-----------------|-------------|
| diagnosis.active | true | ROVER_DIAGNOSIS_ACTIVE | Is active the diagnosis gRPC server. |
| diagnosis.host | 127.0.0.1 | ROVER_DIAGNOSIS_HOST | The bind host of the diagnosis gRPC server. |
| diagnosis.port | 12700 | ROVER_DIAGNOSIS_PORT | The bind port of the diagnosis gRPC server. |

## rover-cli

The `rover-cli` binary is shipped in the rover docker image.

\```bash
# list the discovered processes
/rover-cli process list --service gateway

# trace the TCP connect operations
/rover-cli trace connect --service gateway --duration 60s
\```
```

（实际写入时去掉 code fence 的转义反斜杠；若目录内既有文档的表格列名不同，以目录内风格为准。）

- [ ] **Step 2: Commit**

```bash
git add docs/en/setup/configuration/diagnosis.md
git commit -m "docs: add diagnosis module configuration document"
```

---

### Task 14: lint 与整体验证

- [ ] **Step 1: 运行 lint 与全量测试**

```bash
make lint
go test ./pkg/diagnosis/... ./cmd/cli/...
GOOS=linux go build ./...
```

预期：lint 无新增告警（License 头检查通过），测试全部 PASS。有问题修复后再 commit。

- [ ] **Step 2: Commit（如有修复）**

```bash
git add -A
git commit -m "chore: fix lint issues for diagnosis module and rover-cli"
```

---

### Task 15: 自动化 e2e 用例

**Files:**
- Create: `test/e2e/cases/diagnosis/e2e.yaml`
- Create: `test/e2e/cases/diagnosis/kind.yaml`（从 `cases/process/istio/kind.yaml` 复制）
- Create: `test/e2e/cases/diagnosis/rover.yaml`（从 `cases/process/istio/rover.yaml` 复制）
- Create: `test/e2e/cases/diagnosis/expected/process.yml`（从 `cases/process/istio/expected/process.yml` 复制）
- Create: `test/e2e/cases/diagnosis/expected/cli-process-list.yml`
- Create: `test/e2e/cases/diagnosis/expected/cli-trace-connect.yml`
- Modify: `.github/workflows/rover.yaml`

- [ ] **Step 1: 复制基础设施文件**

```bash
mkdir -p test/e2e/cases/diagnosis/expected
cp test/e2e/cases/process/istio/kind.yaml test/e2e/cases/diagnosis/
cp test/e2e/cases/process/istio/rover.yaml test/e2e/cases/diagnosis/
cp test/e2e/cases/process/istio/expected/process.yml test/e2e/cases/diagnosis/expected/
```

查看 `rover.yaml` 确认 rover 的资源类型与名称（预期为 default 命名空间下的 DaemonSet `skywalking-rover`），后续 `kubectl exec` 命令以实际名称为准。

- [ ] **Step 2: 编写 e2e.yaml**

`test/e2e/cases/diagnosis/e2e.yaml`：setup 部分整体复制 `cases/process/istio/e2e.yaml`（仅把 rover.yaml 路径改为 `test/e2e/cases/diagnosis/rover.yaml`），verify 部分替换为：

```yaml
verify:
  retry:
    count: 20
    interval: 10s
  cases:
    # ensure the process discovery works(reuse the process case verification)
    - query: |
        swctl --display yaml --base-url=http://${service_skywalking_ui_host}:${service_skywalking_ui_80}/graphql process list --service-name=e2e::productpage.default --instance-name=$( \
          swctl --display yaml --base-url=http://${service_skywalking_ui_host}:${service_skywalking_ui_80}/graphql instance list --service-name=e2e::productpage.default |yq e '.[0].name' - \
        )
      expected: expected/process.yml
    # rover-cli process list
    - query: |
        kubectl exec -n default daemonset/skywalking-rover -- /rover-cli process list --service productpage --format yaml
      expected: expected/cli-process-list.yml
    # rover-cli trace connect: generate a connect from the productpage process during tracing
    - query: |
        (sleep 3 && kubectl exec -n default deploy/productpage-v1 -- python -c "import urllib.request; urllib.request.urlopen('http://details:9080/details/0')" > /dev/null 2>&1 || true) &
        kubectl exec -n default daemonset/skywalking-rover -- /rover-cli trace connect --service productpage --duration 10s --format yaml | yq ea '[.]' -
      expected: expected/cli-trace-connect.yml
```

- [ ] **Step 3: 编写 expected 模板**

`test/e2e/cases/diagnosis/expected/cli-process-list.yml`（带 `#` License 头）：

```yaml
{{- contains . }}
- pid: {{ notEmpty (.pid | toString) }}
  processName: python
  serviceName: {{ notEmpty .serviceName }}
  instanceName: {{ notEmpty .instanceName }}
{{- end }}
```

`test/e2e/cases/diagnosis/expected/cli-trace-connect.yml`：

```yaml
{{- contains . }}
- pid: {{ notEmpty (.pid | toString) }}
  processName: python
  remoteAddress: {{ notEmpty .remoteAddress }}
  success: true
{{- end }}
```

注意：模板函数（`contains` / `notEmpty` / `toString`）以 skywalking-infra-e2e 实际支持为准，若 `toString` 不可用，对数值字段直接断言存在性的写法参照仓库内其他 expected 文件调整。

- [ ] **Step 4: 注册到 GitHub Actions e2e matrix**

`.github/workflows/rover.yaml` 的 `e2e-test` job 的 `matrix.test` 列表中追加（与现有条目同级、同风格）：

```yaml
          - name: Diagnosis
            base: test/e2e/cases/diagnosis
            config: e2e.yaml
```

- [ ] **Step 5: 本地校验 yaml 语法**

```bash
yq e '.' test/e2e/cases/diagnosis/e2e.yaml > /dev/null && echo OK
yq e '.' .github/workflows/rover.yaml > /dev/null && echo OK
```

预期：两个 OK。（完整 e2e 在 CI 执行；若需本地跑，参照 test/e2e 的 README/Makefile target。）

- [ ] **Step 6: Commit**

```bash
git add test/e2e/cases/diagnosis .github/workflows/rover.yaml
git commit -m "test: add diagnosis e2e case for rover-cli"
```

---

### Task 16: 手动端到端验证（开发自查清单）

前置条件：一台可加载 eBPF 的 Linux 环境（或 k8s 集群）+ 可选的 OAP 后端
（无后端时 process list 仍应展示本地发现的进程，`REPORTED` 列为 false）。

- [ ] **Step 1: 构建镜像**

```bash
make docker
```

- [ ] **Step 2: 部署 rover**（按现有部署方式，diagnosis 默认开启）

- [ ] **Step 3: 验证 process list**

```bash
kubectl exec -it <rover-pod> -- /rover-cli process list
kubectl exec -it <rover-pod> -- /rover-cli process list --service <已知服务名> --format yaml
```

预期：能列出本地发现的进程，过滤生效；REPORTED 列正确反映上报状态。

- [ ] **Step 4: 验证 trace connect（成功场景）**

选一个会周期性发起对外连接的进程（如 envoy）：

```bash
kubectl exec -it <rover-pod> -- /rover-cli trace connect --pid <pid> --duration 30s
```

预期：实时打印 SUCCESS 事件（TIME 列为真实墙钟时间），30s 后输出统计汇总并退出。

- [ ] **Step 5: 验证 trace connect（失败场景）**

在目标进程容器内主动连接一个不通的地址（如 `curl --max-time 1 http://10.255.255.1:9999`）。

预期：出现 FAILED 事件（errno 为 ECONNREFUSED/ETIMEDOUT 等），统计中 FAIL% 正确。

- [ ] **Step 6: 验证生命周期**

trace 运行中 Ctrl+C，确认 CLI 输出统计后退出；daemon 日志出现 "trace connect stopped"；用 `bpftool prog list | grep diagnosis`（如可用）确认 BPF 程序已卸载。

- [ ] **Step 7: 验证多匹配与 NotFound**

```bash
/rover-cli trace connect --service <匹配多进程的名称> --duration 10s   # 事件中应出现多个 pid
/rover-cli trace connect --service not-exist-name                     # 应报 NotFound 错误
```

---

## 后续任务（本计划范围外）

- 控制接口认证 / TLS
- 更多诊断任务类型（trace dns、trace http、profiling 类能力，复用 `ProcessSelector`）
- e2e 失败场景覆盖（连接拒绝/超时的自动化断言）
