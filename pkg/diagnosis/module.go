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

package diagnosis

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"

	"google.golang.org/grpc"

	"github.com/apache/skywalking-rover/pkg/module"
	"github.com/apache/skywalking-rover/pkg/process"
	diagnosisv1 "github.com/apache/skywalking-rover/proto/diagnosis"
)

const ModuleName = "diagnosis"

type Module struct {
	config *Config

	grpcServer *grpc.Server
	// shutdown is read by the serve goroutine and written by Shutdown,
	// use atomic to avoid the data race between them
	shutdown atomic.Bool
}

func NewModule() *Module {
	return &Module{config: &Config{}}
}

func (m *Module) Name() string {
	return ModuleName
}

func (m *Module) RequiredModules() []string {
	return []string{process.ModuleName}
}

func (m *Module) Config() module.ConfigInterface {
	return m.config
}

func (m *Module) Start(_ context.Context, mgr *module.Manager) error {
	processModule := mgr.FindModule(process.ModuleName).(*process.Module)
	// default to localhost rather than binding all interfaces when the host
	// is empty, the server has no authentication yet
	if m.config.Host == "" {
		m.config.Host = "127.0.0.1"
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", m.config.Host, m.config.Port))
	if err != nil {
		return fmt.Errorf("the diagnosis server listen failure: %v", err)
	}
	m.grpcServer = grpc.NewServer()
	diagnosisv1.RegisterDiagnosisServiceServer(m.grpcServer, newServer(processModule))
	go func() {
		if serveErr := m.grpcServer.Serve(listener); serveErr != nil && !m.shutdown.Load() {
			mgr.ShutdownModules(serveErr)
		}
	}()
	log.Infof("the diagnosis server is listening on %s:%d", m.config.Host, m.config.Port)
	return nil
}

func (m *Module) NotifyStartSuccess() {
}

func (m *Module) Shutdown(_ context.Context, _ *module.Manager) error {
	m.shutdown.Store(true)
	if m.grpcServer != nil {
		m.grpcServer.Stop()
	}
	return nil
}
