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

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/apache/skywalking-rover/pkg/diagnosis/matcher"
	traceconnect "github.com/apache/skywalking-rover/pkg/diagnosis/trace/connect"
	"github.com/apache/skywalking-rover/pkg/logger"
	processapi "github.com/apache/skywalking-rover/pkg/process/api"
	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

var log = logger.GetLogger("diagnosis")

// processLister abstracts the process module for testing
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

// selectorToCondition converts the proto selector (nullable) to the matcher condition
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

// selectorHasCondition reports whether the selector carries at least one filter.
// a tracing task requires a non-empty selector(server-side enforcement, so a
// non-CLI client cannot bypass the CLI's own check and trace every process).
func selectorHasCondition(selector *diagnosisv1.ProcessSelector) bool {
	return selector != nil && (selector.ServiceName != "" || selector.InstanceName != "" ||
		selector.ProcessName != "" || len(selector.Pids) > 0)
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

func (s *server) TraceConnect(req *diagnosisv1.TraceConnectRequest, stream diagnosisv1.DiagnosisService_TraceConnectServer) error {
	if !selectorHasCondition(req.Selector) {
		return status.Error(codes.InvalidArgument,
			"at least one of service_name, instance_name, process_name, pids is required")
	}
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
		case event := <-events:
			if sendErr := stream.Send(buildConnectEvent(event, processNames)); sendErr != nil {
				return sendErr
			}
		}
	}
}

// buildConnectEvent maps a BPF trace connect event to the proto message.
// extracted as a pure function so the mapping is unit-testable without BPF.
func buildConnectEvent(event *traceconnect.Event, processNames map[uint32]string) *diagnosisv1.ConnectEvent {
	return &diagnosisv1.ConnectEvent{
		TimestampNs:   event.Timestamp().UnixNano(),
		Pid:           int32(event.PID),
		ProcessName:   processNames[event.PID],
		RemoteAddress: event.RemoteAddress(),
		Success:       event.Success == 1,
		Errno:         int32(event.ErrorCode),
		LatencyNs:     int64(event.Latency()),
		ErrorMessage:  event.ErrorMessage(),
	}
}
