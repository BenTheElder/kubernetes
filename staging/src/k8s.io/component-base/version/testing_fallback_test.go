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

package version

import (
	"testing"
)

func TestGetMajorMinor(t *testing.T) {
	major, minor, err := getMajorMinor()
	if err != nil {
		t.Fatalf("Failed to get major minor: %v", err)
	}
	if major == "" {
		t.Fatalf("Expected non-empty major version")
	}
	if minor == "" {
		t.Fatalf("Expected non-empty minor version")
	}
	t.Logf("major: %q, minor: %q", major, minor)
}
