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
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	serverAddr  string
	dialTimeout time.Duration
)

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "rover-cli",
		Short:        "rover-cli is a real-time diagnosis tool for the skywalking-rover",
		SilenceUsage: true,
	}
	cmd.PersistentFlags().StringVar(&serverAddr, "addr", "127.0.0.1:12700",
		"the address of the rover diagnosis server")
	cmd.PersistentFlags().DurationVar(&dialTimeout, "timeout", 10*time.Second,
		"the timeout (e.g. 5s, 1m) for connecting to the diagnosis server, "+
			"it also bounds the process list request but not the trace duration")
	cmd.AddCommand(newProcessCmd())
	cmd.AddCommand(newTraceCmd())
	return cmd
}

// dialServer connects to the diagnosis server and waits until the connection
// is ready within the dial timeout, so an unreachable server fails fast
// instead of blocking on the lazy gRPC connect.
func dialServer() (*grpc.ClientConn, error) {
	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			return conn, nil
		}
		if !conn.WaitForStateChange(ctx, state) {
			_ = conn.Close()
			return nil, fmt.Errorf("connect to the diagnosis server %s timeout after %s", serverAddr, dialTimeout)
		}
	}
}
