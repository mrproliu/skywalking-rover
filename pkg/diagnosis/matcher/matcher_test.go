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
