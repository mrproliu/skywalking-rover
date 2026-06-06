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
	"os"

	"github.com/spf13/cobra"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

func newProcessCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "process",
		Short: "explore the processes discovered by the rover",
	}
	cmd.AddCommand(newProcessListCmd())
	return cmd
}

func newProcessListCmd() *cobra.Command {
	var service, instance, processName, format string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the discovered processes",
		RunE: func(*cobra.Command, []string) error {
			if err := validateFormat(format, formatTable, formatJSON, formatYAML); err != nil {
				return err
			}
			conn, err := dialServer()
			if err != nil {
				return err
			}
			defer conn.Close()
			ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
			defer cancel()
			resp, err := diagnosisv1.NewDiagnosisServiceClient(conn).ListProcesses(ctx, &diagnosisv1.ListProcessesRequest{
				Selector: &diagnosisv1.ProcessSelector{
					ServiceName:  service,
					InstanceName: instance,
					ProcessName:  processName,
				},
			})
			if err != nil {
				return err
			}
			return printProcesses(os.Stdout, resp.Processes, format)
		},
	}
	cmd.Flags().StringVar(&service, "service", "", "filter by the service name(substring matching)")
	cmd.Flags().StringVar(&instance, "instance", "", "filter by the instance name(substring matching)")
	cmd.Flags().StringVar(&processName, "process", "", "filter by the process name(substring matching)")
	cmd.Flags().StringVar(&format, "format", formatTable, `the output format, support "table", "json" and "yaml"`)
	return cmd
}
