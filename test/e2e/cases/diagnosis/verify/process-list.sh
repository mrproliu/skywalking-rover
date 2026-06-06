#!/bin/bash

# Licensed to the Apache Software Foundation (ASF) under one or more
# contributor license agreements.  See the NOTICE file distributed with
# this work for additional information regarding copyright ownership.
# The ASF licenses this file to You under the Apache License, Version 2.0
# (the "License"); you may not use this file except in compliance with
# the License.  You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# rover is a DaemonSet, each pod only discovers the processes on its own node,
# so locate the rover pod that runs on the same node as the productpage pod.
set -euo pipefail

NODE=$(kubectl get pod -n default -l app=productpage -o jsonpath='{.items[0].spec.nodeName}')
[ -n "$NODE" ] || { echo "no productpage pod found" >&2; exit 1; }
ROVER_POD=$(kubectl get pod -n default -l name=skywalking-rover \
  --field-selector spec.nodeName="$NODE" -o jsonpath='{.items[0].metadata.name}')
[ -n "$ROVER_POD" ] || { echo "no rover pod on node $NODE" >&2; exit 1; }

kubectl exec -n default "$ROVER_POD" -- /rover-cli process list --service productpage --format yaml
