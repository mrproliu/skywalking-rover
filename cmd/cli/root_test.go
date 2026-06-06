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
	"testing"
	"time"
)

// TestTimeoutFlagDefault verifies the --timeout flag defaults to 10s.
func TestTimeoutFlagDefault(t *testing.T) {
	cmd := newRootCmd()
	got, err := cmd.PersistentFlags().GetDuration("timeout")
	if err != nil {
		t.Fatalf("get timeout failure: %v", err)
	}
	if got != 10*time.Second {
		t.Fatalf("default timeout: want 10s, got %s", got)
	}
}

// TestTimeoutFlagParse verifies the --timeout flag accepts the duration notation.
func TestTimeoutFlagParse(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
	}{
		{input: "5s", want: 5 * time.Second},
		{input: "1m", want: time.Minute},
		{input: "500ms", want: 500 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cmd := newRootCmd()
			if err := cmd.PersistentFlags().Set("timeout", tt.input); err != nil {
				t.Fatalf("set timeout %q failure: %v", tt.input, err)
			}
			got, err := cmd.PersistentFlags().GetDuration("timeout")
			if err != nil {
				t.Fatalf("get timeout failure: %v", err)
			}
			if got != tt.want {
				t.Fatalf("timeout %q: want %s, got %s", tt.input, tt.want, got)
			}
		})
	}
}

// TestDialServerTimeout verifies dialServer fails fast when the server
// is unreachable, bounded by the dial timeout rather than blocking.
func TestDialServerTimeout(t *testing.T) {
	serverAddr = "127.0.0.1:1" // nothing listens here
	dialTimeout = 500 * time.Millisecond

	start := time.Now()
	conn, err := dialServer()
	elapsed := time.Since(start)
	if conn != nil {
		_ = conn.Close()
	}
	if err == nil {
		t.Fatal("want a timeout error for the unreachable server, got nil")
	}
	// should return roughly within the dial timeout, allow generous slack
	if elapsed > 5*time.Second {
		t.Fatalf("dialServer did not fail fast, elapsed %s", elapsed)
	}
}
