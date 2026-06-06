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
