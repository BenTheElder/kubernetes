/*
Copyright The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package policyconfig

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// AdmissionPolicyConfiguration provides configuration for the admission policy controllers.
type AdmissionPolicyConfiguration struct {
	metav1.TypeMeta

	// StaticManifestsDir is the path to a directory containing static
	// admission policy and binding resources to be loaded at startup.
	// The directory should contain YAML or JSON files with these resources.
	// This field is only used when the ManifestBasedAdmissionControlConfig
	// feature gate is enabled.
	// +optional
	StaticManifestsDir string
}
