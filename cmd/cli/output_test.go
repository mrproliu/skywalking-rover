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

import (
	"bytes"
	"strings"
	"testing"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func TestPrintProcessesTable(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{
		{Pid: 1234, Layer: "MESH_DP", ServiceName: "gateway.default",
			InstanceName: "gateway-0", ProcessName: "envoy",
			Labels: []string{"mesh-envoy"}, ReportedToBackend: true},
	}
	if err := printProcesses(&buf, processes, "table"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"PID", "SERVICE", "REPORTED", "1234", "gateway.default", "envoy", "mesh-envoy", "true"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q, got:\n%s", want, out)
		}
	}
}

func TestPrintProcessesJSON(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{{Pid: 1234, ServiceName: "gateway.default"}}
	if err := printProcesses(&buf, processes, "json"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	// normalize whitespace to handle protojson formatting variations
	normalized := strings.ReplaceAll(out, " ", "")
	if !strings.Contains(normalized, `"pid":1234`) || !strings.Contains(normalized, `"serviceName":"gateway.default"`) {
		t.Fatalf("unexpected json output:\n%s", out)
	}
}

func TestPrintProcessesYAML(t *testing.T) {
	var buf bytes.Buffer
	processes := []*diagnosisv1.ProcessInfo{{Pid: 1234, ServiceName: "gateway.default"}}
	if err := printProcesses(&buf, processes, "yaml"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "pid: 1234") || !strings.Contains(out, "serviceName: gateway.default") {
		t.Fatalf("unexpected yaml output:\n%s", out)
	}
}

func TestValidateFormat(t *testing.T) {
	if err := validateFormat(formatJSON, formatTable, formatJSON, formatYAML); err != nil {
		t.Fatalf("json should be valid for process list: %v", err)
	}
	if err := validateFormat(formatText, formatText, formatJSON, formatYAML); err != nil {
		t.Fatalf("text should be valid for trace: %v", err)
	}
	if err := validateFormat("xml", formatText, formatJSON, formatYAML); err == nil {
		t.Fatal("xml should be rejected")
	}
	// table is not a valid trace format
	if err := validateFormat(formatTable, formatText, formatJSON, formatYAML); err == nil {
		t.Fatal("table should be rejected for trace")
	}
}

func TestFormatEventText(t *testing.T) {
	// with the daemon-resolved error message, display the errno symbol name
	event := &diagnosisv1.ConnectEvent{
		TimestampNs: 0, Pid: 1234, ProcessName: "envoy",
		RemoteAddress: "10.96.0.11:443", Success: false, Errno: 111, LatencyNs: 300000,
		ErrorMessage: "ECONNREFUSED: connection refused",
	}
	out := formatEventText(event)
	for _, want := range []string{"1234", "envoy", "10.96.0.11:443", "FAILED(ECONNREFUSED)", "300µs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q, got: %s", want, out)
		}
	}

	// without the error message, fall back to the numeric errno
	event.ErrorMessage = ""
	if out := formatEventText(event); !strings.Contains(out, "FAILED(errno=111)") {
		t.Fatalf("output missing errno fallback, got: %s", out)
	}
}

func TestPrintEventJSONYAML(t *testing.T) {
	event := &diagnosisv1.ConnectEvent{
		Pid: 1234, ProcessName: "envoy", RemoteAddress: "10.96.0.11:443",
		Success: true, Errno: 0,
	}

	var jb bytes.Buffer
	if err := printEvent(&jb, event, formatJSON); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(jb.String(), `"remoteAddress":"10.96.0.11:443"`) ||
		!strings.Contains(jb.String(), `"success":true`) {
		t.Fatalf("unexpected json event:\n%s", jb.String())
	}

	var yb bytes.Buffer
	if err := printEvent(&yb, event, formatYAML); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	out := yb.String()
	if !strings.HasPrefix(out, "---\n") || !strings.Contains(out, "remoteAddress: 10.96.0.11:443") ||
		!strings.Contains(out, "processName: envoy") {
		t.Fatalf("unexpected yaml event:\n%s", out)
	}
}

func TestPrintStats(t *testing.T) {
	collector := newStatsCollector()
	collector.Add("10.96.0.10:9090", true)
	collector.Add("10.96.0.11:443", false)

	// table
	var tb bytes.Buffer
	if err := printStats(&tb, collector, formatText); err != nil {
		t.Fatalf("table: %v", err)
	}
	for _, want := range []string{"REMOTE", "TOTAL", "FAIL%", "10.96.0.10:9090", "100%"} {
		if !strings.Contains(tb.String(), want) {
			t.Fatalf("table stats missing %q:\n%s", want, tb.String())
		}
	}

	// json
	var jb bytes.Buffer
	if err := printStats(&jb, collector, formatJSON); err != nil {
		t.Fatalf("json: %v", err)
	}
	if !strings.Contains(jb.String(), `"statistics"`) || !strings.Contains(jb.String(), `"remote":"10.96.0.11:443"`) {
		t.Fatalf("unexpected json stats:\n%s", jb.String())
	}

	// yaml
	var yb bytes.Buffer
	if err := printStats(&yb, collector, formatYAML); err != nil {
		t.Fatalf("yaml: %v", err)
	}
	if !strings.Contains(yb.String(), "statistics:") || !strings.Contains(yb.String(), "remote: 10.96.0.10:9090") {
		t.Fatalf("unexpected yaml stats:\n%s", yb.String())
	}
}
