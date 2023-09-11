/*
Copyright 2023 The Kubernetes Authors.

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

package main

import (
	"errors"
	"fmt"
	"go/build"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/tools/cover"
	apiextensionsclientsetscheme "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/scheme"
	"k8s.io/apimachinery/pkg/util/sets"
	clientsetscheme "k8s.io/client-go/kubernetes/scheme"
	aggregatorclientsetscheme "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset/scheme"
)

// Should be run after OpenAPI spec has been generated.
//
// For each API type published in OpenAPI, the corresponding internal type
// should have a validate package rooted at the type's pkgPath/validation
//
// Running tests in that package should yield 100% coverage

func newCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check_coverage <openapi-spec-dir> <apis_dir>",
		Short: `Checks validation tests in API packages should yield 100% coverage`,
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 2 {
				cmd.Help()
				os.Exit(1)
			}

			openAPISpecPath := args[0]
			apisDir := args[1]
			err := processSpec(openAPISpecPath, apisDir)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		},
	}
	return cmd
}

func main() {
	command := newCommand()
	if err := command.Execute(); err != nil {
		os.Exit(1)
	}
}

func processSpec(openAPISpecDir, apisDir string) error {
	aggregatorclientsetscheme.AddToScheme(clientsetscheme.Scheme)
	apiextensionsclientsetscheme.AddToScheme(clientsetscheme.Scheme)

	// List directories for list of groups
	entries, err := os.ReadDir(openAPISpecDir)
	if err != nil {
		return err
	}

	profilesTemp, err := os.MkdirTemp(os.TempDir(), "check_coverage")
	if err != nil {
		return err
	}
	defer os.RemoveAll(profilesTemp)

	searchPaths := []string{
		apisDir,
		"staging/src/k8s.io/apiextensions-apiserver/pkg/apis",
		"staging/src/k8s.io/kube-aggregator/pkg/apis",
	}

	suf := "_openapi.json"
	pre1 := "apis__"
	pre2 := "api__"

	groups := sets.Set[string]{}
	for _, f := range entries {
		name := f.Name()

		if !strings.HasSuffix(name, suf) {
			continue
		} else if !strings.HasPrefix(name, pre1) && !strings.HasPrefix(name, pre2) {
			continue
		}

		trimmed := strings.TrimSuffix(name, suf)
		trimmed = strings.TrimPrefix(trimmed, pre1)
		trimmed = strings.TrimPrefix(trimmed, pre2)

		group, _, hasVersion := strings.Cut(trimmed, "__")
		if !hasVersion {
			if strings.HasPrefix(name, pre2) {
				// version = group
				group = "core"
			} else {
				continue
			}
		}

		groups.Insert(group)
	}

	// Validation Existence Exceptions
	validationExistenceExceptions := sets.New(
		// Internal types for events.k8s.io are defined and validated in core
		// package, not events package
		"events.k8s.io",
	)

	// If any uncovered lones contains this
	blacklistedMissedValidationLines := sets.New(
		"allErrs = append(allErrs,",
	)

	var searchErrors []error
	for group := range groups {
		if validationExistenceExceptions.Has(group) {
			continue
		}

		fmt.Printf("Checking %v\n", group)

		// Find internal type
		withoutSuffix := strings.TrimSuffix(group, ".authorization.k8s.io")
		withoutSuffix = strings.TrimSuffix(withoutSuffix, ".k8s.io")

		// Naming Exceptions
		if group == "internal.apiserver.k8s.io" {
			withoutSuffix = "apiserverinternal"
		}
		if group == "flowcontrol.apiserver.k8s.io" {
			withoutSuffix = "flowcontrol"
		}

		searchedPaths := []string{}
		found := false
		foundPath := ""

		for _, p := range searchPaths {
			candidatePath := filepath.Join(p, withoutSuffix)
			searchedPaths = append(searchedPaths, candidatePath)
			_, err := os.Stat(candidatePath)
			if err == nil {
				found = true
				foundPath = candidatePath
				break
			}
		}

		_ = foundPath
		if !found {
			err := fmt.Errorf("failed to find group %s in search paths: %v", group, searchedPaths)
			fmt.Println(err)
			searchErrors = append(searchErrors, err)
			continue
		}

		validationPackagePath := filepath.Join(foundPath, "validation")
		if _, err := os.Stat(validationPackagePath); err != nil {
			fmt.Println(err)
			searchErrors = append(searchErrors, err)
			continue
		}

		// Validation package exists. Run go text -cover on it and determine
		// coverage level
		profilePath := filepath.Join(profilesTemp, group+"_cover.out")
		var out strings.Builder
		cmd := exec.Command("go", "test", "./", "-coverpkg", "./", "-coverprofile", profilePath)
		cmd.Dir = validationPackagePath
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			err = fmt.Errorf("%v\n%w", out.String(), err)
			fmt.Println(err)
			searchErrors = append(searchErrors, err)
			continue
		}

		profiles, err := cover.ParseProfiles(profilePath)
		if err != nil {
			fmt.Println(err)
			searchErrors = append(searchErrors, err)
			continue
		}

		missedLines := []string{}

		// Loop through all unhit blocks, and check whether they contain
		// any code that could possibly be an error
		for _, p := range profiles {
			// Resolve the go package filename to source
			dir, err := build.Default.Import(filepath.Dir(p.FileName), ".", build.FindOnly)

			if err != nil {
				fmt.Println(err)
				searchErrors = append(searchErrors, err)
				continue
			}
			filePath := filepath.Join(dir.Dir, filepath.Base(p.FileName))

			if strings.HasPrefix(filePath, "k8s.io/kubernetes/") {
				filePath = strings.TrimPrefix(p.FileName, "k8s.io/kubernetes/")
			} else if strings.HasPrefix(filePath, "k8s.io/apiextensions-apiserver") {
				filePath = "staging/src/" + filePath
			}
			fileText, err := os.ReadFile(filePath)
			if err != nil {
				fmt.Println(err)
				searchErrors = append(searchErrors, err)
				continue
			}

			boundaries := p.Boundaries(fileText)
			for i, block := range p.Blocks {
				startBoundary := boundaries[i*2]
				endBoundary := boundaries[i*2+1]

				if block.Count != 0 {
					continue
				}

				substr := string(fileText[startBoundary.Offset:endBoundary.Offset])

				// HEURISTIC: There are a few untested ValidateXUpdate
				// functions that just defer to the regular Validate function.
				// If we find a block that matches that pattern, and the function
				// is tiny enough, skip
				//!TODO: Decide if thats what we actually want to do
				pat := regexp.MustCompile(`^\s*func\s*Validate[a-zA-Z0-9]*Update.*`)
				if pat.Match([]byte(substr)) && block.NumStmt < 5 {
					continue
				}

				for blacklisted := range blacklistedMissedValidationLines {
					if strings.Contains(substr, blacklisted) {
						missedLines = append(missedLines, fmt.Sprintf("%s:%v:%v-%v:%v", filePath, block.StartLine, block.StartCol, block.EndLine, block.EndCol))
						break
					}
				}
			}
		}

		if len(missedLines) > 0 {
			err := strings.Join(missedLines, "\n")
			fmt.Println(err)
			// searchErrors = append(searchErrors, err)
			continue
		}

		fmt.Println("pass")
	}

	if len(searchErrors) > 0 {
		return errors.Join(searchErrors...)
	}

	return nil
}
