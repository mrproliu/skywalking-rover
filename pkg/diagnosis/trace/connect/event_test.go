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

package connect

import (
	"encoding/binary"
	"testing"
	"time"
)

func TestEventRemoteAddress(t *testing.T) {
	// IPv4: 10.96.0.10:8080
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
	if got := unknown.RemoteAddress(); got != unknownAddress {
		t.Fatalf("want unknown, got %s", got)
	}
}

func TestEventLatency(t *testing.T) {
	e := &Event{StartTime: 1000, EndTime: 301000}
	if got := e.Latency(); got != 300*time.Microsecond {
		t.Fatalf("want 300µs, got %s", got)
	}

	// out-of-order timestamps must not underflow into a huge duration
	reversed := &Event{StartTime: 301000, EndTime: 1000}
	if got := reversed.Latency(); got != 0 {
		t.Fatalf("want 0 for reversed timestamps, got %s", got)
	}
}

// NOTE: the expectations are based on the Linux errno table,
// this test only runs on Linux(same as the daemon runtime)
func TestEventErrorMessage(t *testing.T) {
	// success event has no error message
	success := &Event{Success: 1}
	if got := success.ErrorMessage(); got != "" {
		t.Fatalf("want empty error message, got %s", got)
	}

	// ECONNREFUSED(111 on Linux) resolves to the symbol name with text
	refused := &Event{Success: 0, ErrorCode: 111}
	if got := refused.ErrorMessage(); got != "ECONNREFUSED: connection refused" {
		t.Fatalf("want ECONNREFUSED: connection refused, got %s", got)
	}
}
