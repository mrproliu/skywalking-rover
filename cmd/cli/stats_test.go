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

import "testing"

func TestStatsCollector(t *testing.T) {
	collector := newStatsCollector()
	collector.Add("10.96.0.10:9090", true)
	collector.Add("10.96.0.10:9090", true)
	collector.Add("10.96.0.11:443", false)

	rows := collector.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows, got %d", len(rows))
	}
	first := rows[0]
	if first.Remote != "10.96.0.10:9090" || first.Total != 2 || first.Success != 2 ||
		first.Failed != 0 || first.FailPercent != 0 {
		t.Fatalf("unexpected first row: %+v", first)
	}
	second := rows[1]
	if second.Remote != "10.96.0.11:443" || second.Total != 1 || second.Failed != 1 ||
		second.FailPercent != 100 {
		t.Fatalf("unexpected second row: %+v", second)
	}
}
