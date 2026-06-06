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
