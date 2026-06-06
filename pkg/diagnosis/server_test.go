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

package diagnosis

import (
	"context"
	"encoding/binary"
	"testing"

	"github.com/shirou/gopsutil/process"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	traceconnect "github.com/apache/skywalking-rover/pkg/diagnosis/trace/connect"
	processapi "github.com/apache/skywalking-rover/pkg/process/api"
	"github.com/apache/skywalking-rover/pkg/tools/profiling"
	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
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
		// reported to the backend
		&fakeProcess{pid: 1, id: "id-1", entity: &processapi.ProcessEntity{
			Layer: "MESH_DP", ServiceName: "gateway.default", InstanceName: "gateway-0",
			ProcessName: "envoy", Labels: []string{"mesh-envoy"},
		}},
		// discovered locally but not reported yet (empty id)
		&fakeProcess{pid: 2, id: "", entity: &processapi.ProcessEntity{
			Layer: "K8S_SERVICE", ServiceName: "order.default", InstanceName: "order-0",
			ProcessName: "java", Labels: []string{"k8s-service"},
		}},
	}}
	server := newServer(lister)

	// not-reported process must still be visible
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

	// reported process carries entity id + flag
	resp, err = server.ListProcesses(context.Background(), &diagnosisv1.ListProcessesRequest{
		Selector: &diagnosisv1.ProcessSelector{ServiceName: "gateway"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Processes) != 1 || !resp.Processes[0].ReportedToBackend || resp.Processes[0].EntityId != "id-1" {
		t.Fatalf("unexpected reported process info: %+v", resp.Processes[0])
	}

	// nil selector returns all
	resp, err = server.ListProcesses(context.Background(), &diagnosisv1.ListProcessesRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Processes) != 2 {
		t.Fatalf("want 2 processes, got %d", len(resp.Processes))
	}
}

// TestTraceConnectNotFound verifies the NotFound status when the selector
// matches no process. this path returns before touching the stream, so a
// nil stream is safe here.
func TestTraceConnectNotFound(t *testing.T) {
	server := newServer(&fakeLister{processes: []processapi.ProcessInterface{
		&fakeProcess{pid: 1, id: "id-1", entity: &processapi.ProcessEntity{ServiceName: "gateway.default"}},
	}})
	err := server.TraceConnect(&diagnosisv1.TraceConnectRequest{
		Selector: &diagnosisv1.ProcessSelector{ServiceName: "not-exist"},
	}, nil)
	if status.Code(err) != codes.NotFound {
		t.Fatalf("want NotFound, got %v", err)
	}
}

// TestTraceConnectInvalidArgument verifies the server rejects an empty selector
// (a non-CLI client must not be able to trace every process). nil stream is
// safe because the check returns before touching the stream.
func TestTraceConnectInvalidArgument(t *testing.T) {
	server := newServer(&fakeLister{})
	cases := []*diagnosisv1.TraceConnectRequest{
		{}, // nil selector
		{Selector: &diagnosisv1.ProcessSelector{}}, // empty selector
		{Selector: &diagnosisv1.ProcessSelector{Pids: nil}},
	}
	for _, req := range cases {
		if err := server.TraceConnect(req, nil); status.Code(err) != codes.InvalidArgument {
			t.Fatalf("want InvalidArgument for %+v, got %v", req, err)
		}
	}
}

// TestBuildConnectEvent verifies the BPF event -> proto mapping for both a
// successful and a failed connect.
func TestBuildConnectEvent(t *testing.T) {
	names := map[uint32]string{1234: "envoy"}

	// successful IPv4 connect
	ok := &traceconnect.Event{
		StartTime: 1000, EndTime: 301000, PID: 1234, Family: 2, Success: 1,
		RemotePort: 9090,
	}
	ok.RemoteAddrV4 = binary.LittleEndian.Uint32([]byte{10, 96, 0, 10})
	got := buildConnectEvent(ok, names)
	if got.Pid != 1234 || got.ProcessName != "envoy" || !got.Success ||
		got.Errno != 0 || got.ErrorMessage != "" ||
		got.RemoteAddress != "10.96.0.10:9090" || got.LatencyNs != 300000 {
		t.Fatalf("unexpected success event mapping: %+v", got)
	}

	// failed connect carries errno + message, no process name match
	fail := &traceconnect.Event{
		StartTime: 0, EndTime: 0, PID: 5678, Family: 2, Success: 0, ErrorCode: 111,
		RemotePort: 443,
	}
	fail.RemoteAddrV4 = binary.LittleEndian.Uint32([]byte{10, 96, 0, 11})
	got = buildConnectEvent(fail, names)
	if got.Pid != 5678 || got.ProcessName != "" || got.Success ||
		got.Errno != 111 || got.ErrorMessage == "" {
		t.Fatalf("unexpected failed event mapping: %+v", got)
	}
}
