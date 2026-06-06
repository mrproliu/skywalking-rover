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
	"fmt"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/apache/skywalking-rover/pkg/tools/btf"
	"github.com/apache/skywalking-rover/pkg/tools/host"
	"github.com/apache/skywalking-rover/pkg/tools/ip"
)

const (
	afInet  = 2
	afInet6 = 10

	// unknownAddress is the remote address placeholder for the unsupported socket family
	unknownAddress = "unknown"
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

// ReadFrom implements the btf.EventReader interface
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

// RemoteAddress builds the remote address string
func (e *Event) RemoteAddress() string {
	switch e.Family {
	case afInet:
		return fmt.Sprintf("%s:%d", ip.ParseIPV4(e.RemoteAddrV4), e.RemotePort)
	case afInet6:
		return fmt.Sprintf("[%s]:%d", ip.ParseIPV6(e.RemoteAddrV6), e.RemotePort)
	}
	return unknownAddress
}

// Timestamp converts the BPF ktime to the real wall-clock time
func (e *Event) Timestamp() time.Time {
	return host.Time(e.EndTime)
}

// Latency is the duration of the connect syscall
func (e *Event) Latency() time.Duration {
	// guard against an unsigned underflow if the events ever arrive out of order
	if e.EndTime < e.StartTime {
		return 0
	}
	return time.Duration(e.EndTime - e.StartTime)
}

// ErrorMessage resolve the errno to the human-readable message,
// the resolving MUST happen on the daemon side since the errno table
// is OS/architecture dependent, e.g. "ECONNREFUSED: connection refused"
func (e *Event) ErrorMessage() string {
	if e.Success == 1 || e.ErrorCode == 0 {
		return ""
	}
	errno := syscall.Errno(e.ErrorCode)
	name := unix.ErrnoName(errno)
	if name == "" {
		return errno.Error()
	}
	return fmt.Sprintf("%s: %s", name, errno.Error())
}
