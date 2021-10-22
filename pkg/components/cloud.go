// Copyright (c) 2021 Tigera, Inc. All rights reserved.

// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package components

// Default registries for Calico and Tigera.
const (
	CloudRegistry = "gcr.io/tigera-tesla"
)

func cloudRegistry(c component, registry string) string {
	if registry == "" || registry == UseDefault {
		switch c {
		case ComponentEsProxy, ComponentIntrusionDetectionController, ComponentTigeraKubeControllers:
			registry = CloudRegistry
		}
	}
	return registry
}
