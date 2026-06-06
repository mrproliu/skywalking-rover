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

// Start load the BPF program and return the event channel. the channel is
// never closed(see Stop): the consumer must stop reading via its own context.
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

// Stop unload the BPF program. the events channel is intentionally NOT closed:
// the async perf reader goroutine may still be inside handleEvent sending to it,
// and closing concurrently would panic with "send on closed channel"(the select
// default does not protect a send on a closed channel). the reader goroutine
// exits on its own once linker.Close closes the perf reader, and the channel is
// left to be garbage collected. the server loop exits via the stream context,
// not via a channel close.
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
	return err
}
