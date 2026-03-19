//go:build !notest

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
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/blang/semver/v4"
)

func init() {
	// Check if we are running in go test, this prevents running in binaries,
	// EVEN IF the build tag was not set due to not using our build scripts.
	//
	// (YOU SHOULD BUILD RELEASES WITH `make release`!).
	//
	// When binaries are built with make, this entire file is compiled out by
	// the notest tag, making this redundant for Kubernetes.
	//
	// Checking helps protect other users of this package
	if !testing.Testing() {
		// do not do anything if we're not running in go test
		return
	}
	// That leaves the case that we are testing with `go test`
	// (we allow testing without using the makefiles)
	// In this case, we will *attempt* to populate version info without failing
	// IFF the version info is not already populated
	// This protects other projects using this package during testing
	if gitMinor != "" || gitMajor != "" {
		return
	}
	// ok now we will detect major and minor verison, so we can avoid hardcoding it
	major, minor, err := getMajorMinor()
	if err == nil {
		gitMajor, gitMinor = major, minor
		DefaultKubeBinaryVersion = gitMajor + "." + gitMinor
		gitVersion = "v" + DefaultKubeBinaryVersion + ".0"
		dynamicGitVersion.Store(gitVersion)
	}
}

func getMajorMinor() (string, string, error) {
	out, err := exec.Command("git", "describe").Output()
	if err != nil {
		return "", "", err
	}
	versionSanitized := strings.TrimPrefix(string(out), "v")
	v, err := semver.ParseTolerant(versionSanitized)
	if err != nil {
		return "", "", err
	}
	return strconv.FormatUint(v.Major, 10), strconv.FormatUint(v.Minor, 10), nil
}
