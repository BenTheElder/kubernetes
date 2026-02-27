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

package config

import (
	"fmt"
	"io"
	"os"
	"path"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apiserver/pkg/admission/plugin/policy/config/apis/policyconfig"
	v1 "k8s.io/apiserver/pkg/admission/plugin/policy/config/apis/policyconfig/v1"
	"k8s.io/apiserver/pkg/features"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
)

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme)
)

func init() {
	utilruntime.Must(policyconfig.AddToScheme(scheme))
	utilruntime.Must(v1.AddToScheme(scheme))
}

// PolicyConfig holds the configuration loaded from the config file.
type PolicyConfig struct {
	// StaticManifestsDir is the path to the directory containing static policy manifests.
	StaticManifestsDir string
}

// LoadConfig extracts the admission policy configuration from configFile.
func LoadConfig(configFile io.Reader) (PolicyConfig, error) {
	var cfg PolicyConfig
	if configFile != nil {
		// we have a config so parse it.
		data, err := io.ReadAll(configFile)
		if err != nil {
			return cfg, err
		}
		decoder := codecs.UniversalDecoder()
		decodedObj, err := runtime.Decode(decoder, data)
		if err != nil {
			return cfg, err
		}
		config, ok := decodedObj.(*policyconfig.AdmissionPolicyConfiguration)
		if !ok {
			return cfg, fmt.Errorf("unexpected type: %T", decodedObj)
		}

		if len(config.StaticManifestsDir) > 0 {
			if !utilfeature.DefaultFeatureGate.Enabled(features.ManifestBasedAdmissionControlConfig) {
				return cfg, field.Forbidden(field.NewPath("staticManifestsDir"), "staticManifestsDir requires the ManifestBasedAdmissionControlConfig feature gate to be enabled")
			}
			if !path.IsAbs(config.StaticManifestsDir) {
				return cfg, field.Invalid(field.NewPath("staticManifestsDir"), config.StaticManifestsDir, "must be an absolute file path")
			}
			info, err := os.Stat(config.StaticManifestsDir)
			if err != nil {
				return cfg, field.Invalid(field.NewPath("staticManifestsDir"), config.StaticManifestsDir, fmt.Sprintf("unable to read: %v", err))
			}
			if !info.IsDir() {
				return cfg, field.Invalid(field.NewPath("staticManifestsDir"), config.StaticManifestsDir, "must be a directory")
			}
		}

		cfg.StaticManifestsDir = config.StaticManifestsDir
	}
	return cfg, nil
}
