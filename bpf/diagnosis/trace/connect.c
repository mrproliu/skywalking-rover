// Licensed to Apache Software Foundation (ASF) under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Apache Software Foundation (ASF) licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// +build ignore

#include "api.h"
#include <linux/in.h>
#include <linux/in6.h>
#include <asm/errno.h>
#include <bpf/bpf_endian.h>

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

// LRU hash so a connect that enters but never exits(the task disappears in
// between) is evicted automatically instead of leaking a slot forever
struct {
    __uint(type, BPF_MAP_TYPE_LRU_HASH);
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
