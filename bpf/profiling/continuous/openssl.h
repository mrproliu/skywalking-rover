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

struct openssl_role_symaddr {
    // read the SSL is server side or not
    __u32 server_offset;
};

struct {
	__uint(type, BPF_MAP_TYPE_HASH);
	__uint(max_entries, 10000);
	__type(key, __u32);
	__type(value, struct openssl_fd_symaddr);
} openssl_role_symaddr_finder SEC(".maps");
static __inline struct openssl_role_symaddr* get_openssl_role_symaddr(__u32 tgid) {
    struct openssl_role_symaddr *addr = bpf_map_lookup_elem(&openssl_role_symaddr_finder, &tgid);
    return addr;
}