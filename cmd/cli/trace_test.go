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
	"io"
	"testing"
	"time"
)

// TestTraceConnectDurationFlag verifies the --duration flag accepts the
// second/minute/hour notations and parses them to the expected durations.
func TestTraceConnectDurationFlag(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "30s", want: 30 * time.Second},
		{input: "5m", want: 5 * time.Minute},
		{input: "1h", want: time.Hour},
		{input: "1h30m", want: 90 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cmd := newTraceConnectCmd()
			if err := cmd.Flags().Set("duration", tt.input); err != nil {
				t.Fatalf("set duration %q failure: %v", tt.input, err)
			}
			got, err := cmd.Flags().GetDuration("duration")
			if err != nil {
				t.Fatalf("get duration failure: %v", err)
			}
			if got != tt.want {
				t.Fatalf("duration %q: want %s, got %s", tt.input, tt.want, got)
			}
		})
	}
}

// TestTraceConnectDurationDefault verifies the default(no flag) is zero,
// which means tracing until interrupted.
func TestTraceConnectDurationDefault(t *testing.T) {
	cmd := newTraceConnectCmd()
	got, err := cmd.Flags().GetDuration("duration")
	if err != nil {
		t.Fatalf("get duration failure: %v", err)
	}
	if got != 0 {
		t.Fatalf("default duration: want 0, got %s", got)
	}
}

// TestTraceConnectRequiresSelector verifies RunE rejects an invocation with no
// selector flag before dialing the server.
func TestTraceConnectRequiresSelector(t *testing.T) {
	cmd := newTraceConnectCmd()
	cmd.SetArgs([]string{})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("want an error when no selector flag is provided, got nil")
	}
}

// TestTraceConnectRejectsBadFormat verifies RunE rejects an unknown --format
// before dialing the server.
func TestTraceConnectRejectsBadFormat(t *testing.T) {
	cmd := newTraceConnectCmd()
	cmd.SetArgs([]string{"--service", "x", "--format", "xml"})
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err == nil {
		t.Fatal("want an error for unknown format, got nil")
	}
}
