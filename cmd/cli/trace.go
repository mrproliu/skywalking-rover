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
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func newTraceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "trace",
		Short: "real-time tracing on the target processes",
	}
	cmd.AddCommand(newTraceConnectCmd())
	return cmd
}

func newTraceConnectCmd() *cobra.Command {
	var service, instance, processName, format string
	var pids []int32
	var duration time.Duration
	cmd := &cobra.Command{
		Use:   "connect",
		Short: "trace the TCP connect operations of the target processes",
		RunE: func(*cobra.Command, []string) error {
			if service == "" && instance == "" && processName == "" && len(pids) == 0 {
				return fmt.Errorf("at least one of --service, --instance, --process, --pid is required")
			}
			if err := validateFormat(format, formatText, formatJSON, formatYAML); err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			if duration > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, duration)
				defer cancel()
			}

			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()
			stream, err := diagnosisv1.NewDiagnosisServiceClient(conn).TraceConnect(ctx, &diagnosisv1.TraceConnectRequest{
				Selector: &diagnosisv1.ProcessSelector{
					ServiceName:  service,
					InstanceName: instance,
					ProcessName:  processName,
					Pids:         pids,
				},
			})
			if err != nil {
				return err
			}

			collector := newStatsCollector()
			if format == formatText {
				printEventHeader(os.Stdout)
			}
			for {
				event, recvErr := stream.Recv()
				if recvErr != nil {
					if statsErr := printStats(os.Stdout, collector, format); statsErr != nil {
						return statsErr
					}
					if isExpectedEnd(recvErr) {
						return nil
					}
					return recvErr
				}
				collector.Add(event.RemoteAddress, event.Success)
				if printErr := printEvent(os.Stdout, event, format); printErr != nil {
					return printErr
				}
			}
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "trace the processes matched with the service name(substring matching)")
	cmd.Flags().StringVar(&instance, "instance", "", "trace the processes matched with the instance name(substring matching)")
	cmd.Flags().StringVar(&processName, "process", "", "trace the processes matched with the process name(substring matching)")
	cmd.Flags().Int32SliceVar(&pids, "pid", nil, "trace the specified pids")
	cmd.Flags().DurationVar(&duration, "duration", 0, "the tracing duration (e.g. 30s, 5m, 1h), "+
		"automatically stop when reached, trace until interrupted(Ctrl+C) if not set")
	cmd.Flags().StringVar(&format, "format", formatText, `the output format, support "text", "json" and "yaml"`)
	return cmd
}

// isExpectedEnd check the stream end is triggered by the user interrupt or duration reached
func isExpectedEnd(err error) bool {
	if errors.Is(err, io.EOF) {
		return true
	}
	s, ok := status.FromError(err)
	if !ok {
		return false
	}
	return s.Code() == codes.Canceled || s.Code() == codes.DeadlineExceeded
}
