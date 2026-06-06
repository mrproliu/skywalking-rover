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
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"sigs.k8s.io/yaml"

	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

const (
	formatTable = "table"
	formatText  = "text"
	formatJSON  = "json"
	formatYAML  = "yaml"
)

// validateFormat checks the --format value against the supported set,
// so an unknown format fails fast instead of silently producing the default.
func validateFormat(format string, allowed ...string) error {
	for _, a := range allowed {
		if format == a {
			return nil
		}
	}
	return fmt.Errorf("unsupported format %q, supported: %s", format, strings.Join(allowed, ", "))
}

func protoToJSON(m proto.Message) ([]byte, error) {
	// EmitUnpopulated keeps the zero-value fields(e.g. errno: 0, reportedToBackend: false)
	// in the output, the diagnosis result should always be complete
	return protojson.MarshalOptions{EmitUnpopulated: true}.Marshal(m)
}

func protoToYAML(m proto.Message) ([]byte, error) {
	jsonData, err := protoToJSON(m)
	if err != nil {
		return nil, err
	}
	return yaml.JSONToYAML(jsonData)
}

func printProcesses(w io.Writer, processes []*diagnosisv1.ProcessInfo, format string) error {
	switch format {
	case formatJSON:
		for _, p := range processes {
			data, err := protoToJSON(p)
			if err != nil {
				return err
			}
			fmt.Fprintln(w, string(data))
		}
		return nil
	case formatYAML:
		// output as a single YAML array for the e2e verification
		items := make([]json.RawMessage, 0, len(processes))
		for _, p := range processes {
			data, err := protoToJSON(p)
			if err != nil {
				return err
			}
			items = append(items, data)
		}
		jsonData, err := json.Marshal(items)
		if err != nil {
			return err
		}
		yamlData, err := yaml.JSONToYAML(jsonData)
		if err != nil {
			return err
		}
		fmt.Fprint(w, string(yamlData))
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PID\tLAYER\tSERVICE\tINSTANCE\tPROCESS\tLABELS\tREPORTED")
	for _, p := range processes {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%t\n",
			p.Pid, p.Layer, p.ServiceName, p.InstanceName, p.ProcessName,
			strings.Join(p.Labels, ","), p.ReportedToBackend)
	}
	return tw.Flush()
}

func printEventHeader(w io.Writer) {
	fmt.Fprintf(w, "%-15s %-8s %-15s %-25s %-18s %s\n",
		"TIME", "PID", "PROCESS", "REMOTE", "RESULT", "LATENCY")
}

func formatEventText(e *diagnosisv1.ConnectEvent) string {
	result := "SUCCESS"
	if !e.Success {
		if name, _, found := strings.Cut(e.ErrorMessage, ":"); found && name != "" {
			result = fmt.Sprintf("FAILED(%s)", name)
		} else if e.ErrorMessage != "" {
			result = fmt.Sprintf("FAILED(%s)", e.ErrorMessage)
		} else {
			result = fmt.Sprintf("FAILED(errno=%d)", e.Errno)
		}
	}
	return fmt.Sprintf("%-15s %-8d %-15s %-25s %-18s %s",
		time.Unix(0, e.TimestampNs).Format("15:04:05.000"),
		e.Pid, e.ProcessName, e.RemoteAddress, result, time.Duration(e.LatencyNs))
}

func printEvent(w io.Writer, e *diagnosisv1.ConnectEvent, format string) error {
	switch format {
	case formatJSON:
		data, err := protoToJSON(e)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
	case formatYAML:
		data, err := protoToYAML(e)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "---\n%s", string(data))
	default:
		fmt.Fprintln(w, formatEventText(e))
	}
	return nil
}

func printStats(w io.Writer, collector *statsCollector, format string) error {
	rows := collector.Rows()
	switch format {
	case formatJSON:
		data, err := json.Marshal(map[string]interface{}{"statistics": rows})
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(data))
		return nil
	case formatYAML:
		jsonData, err := json.Marshal(map[string]interface{}{"statistics": rows})
		if err != nil {
			return err
		}
		yamlData, err := yaml.JSONToYAML(jsonData)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "---\n%s", string(yamlData))
		return nil
	}
	fmt.Fprintln(w, "\n--- tcp connect statistics ---")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "REMOTE\tTOTAL\tSUCCESS\tFAILED\tFAIL%")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%.0f%%\n",
			row.Remote, row.Total, row.Success, row.Failed, row.FailPercent)
	}
	return tw.Flush()
}
