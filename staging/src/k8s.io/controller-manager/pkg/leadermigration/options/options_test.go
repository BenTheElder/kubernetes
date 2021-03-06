/*
Copyright 2021 The Kubernetes Authors.

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

package options

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"

	"github.com/spf13/pflag"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	featuregatetesting "k8s.io/component-base/featuregate/testing"
	"k8s.io/controller-manager/config"
	"k8s.io/controller-manager/pkg/features"
	migrationconfig "k8s.io/controller-manager/pkg/leadermigration/config"
)

func TestLeaderMigrationOptions(t *testing.T) {
	testCases := []struct {
		name              string
		flags             []string
		configContent     string
		expectEnabled     bool
		expectErr         bool
		enableFeatureGate bool
		expectConfig      *config.LeaderMigrationConfiguration
	}{
		{
			name:              "default (disabled), with feature gate disabled",
			flags:             []string{},
			enableFeatureGate: false,
			expectEnabled:     false,
			expectErr:         false,
		},
		{
			name:              "enabled, with feature gate disabled",
			flags:             []string{"--enable-leader-migration"},
			enableFeatureGate: false,
			expectErr:         true,
		},
		{
			name:              "enabled, with default configuration",
			flags:             []string{"--enable-leader-migration"},
			enableFeatureGate: true,
			expectEnabled:     true,
			expectErr:         false,
			expectConfig:      migrationconfig.DefaultLeaderMigrationConfiguration(),
		},
		{
			name:              "enabled, with custom configuration file",
			flags:             []string{"--enable-leader-migration"},
			enableFeatureGate: true,
			expectEnabled:     true,
			configContent: `
apiVersion: controllermanager.config.k8s.io/v1alpha1
kind: LeaderMigrationConfiguration
leaderName: test-leader-migration
resourceLock: leases
controllerLeaders: []
`,
			expectErr: false,
			expectConfig: &config.LeaderMigrationConfiguration{
				LeaderName:        "test-leader-migration",
				ResourceLock:      "leases",
				ControllerLeaders: []config.ControllerLeaderConfiguration{},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			defer featuregatetesting.SetFeatureGateDuringTest(t, utilfeature.DefaultFeatureGate, features.ControllerManagerLeaderMigration, tc.enableFeatureGate)()
			flags := tc.flags
			if tc.configContent != "" {
				configFile, err := ioutil.TempFile("", tc.name)
				if err != nil {
					t.Fatal(err)
				}
				defer os.Remove(configFile.Name())
				err = ioutil.WriteFile(configFile.Name(), []byte(tc.configContent), os.FileMode(0755))
				if err != nil {
					t.Fatal(err)
				}
				flags = append(flags, "--leader-migration-config="+configFile.Name())
			}
			genericConfig := new(config.GenericControllerManagerConfiguration)
			options := new(LeaderMigrationOptions)
			fs := pflag.NewFlagSet("addflagstest", pflag.ContinueOnError)
			options.AddFlags(fs)
			err := fs.Parse(flags)
			if err != nil {
				t.Errorf("cannot parse leader-migration-config: %v", err)
				return
			}
			err = options.ApplyTo(genericConfig)
			if err != nil {
				if !tc.expectErr {
					t.Errorf("unexpected error: %v", err)
					return
				}
				// expect err and got err, finish the test case.
				return
			}
			if err == nil && tc.expectErr {
				t.Errorf("expected error but got nil")
				return
			}
			if genericConfig.LeaderMigrationEnabled != tc.expectEnabled {
				t.Errorf("expected Enabled=%v, got %v", tc.expectEnabled, options.Enabled)
				return
			}
			if tc.expectEnabled && !reflect.DeepEqual(tc.expectConfig, &genericConfig.LeaderMigration) {
				t.Errorf("expected config %#v but got %#v", tc.expectConfig, genericConfig.LeaderMigration)
			}
		})
	}

}
