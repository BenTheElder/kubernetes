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

// Package manifest provides shared utilities for loading admission configurations
// from static manifest files.
package manifest

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// StaticConfigSuffix is the reserved suffix for manifest-based admission configurations.
// Resources with names ending in this suffix can only be created via static manifest
// files loaded at API server startup, not through the REST API.
// NOTE: This constant is duplicated in pkg/apis/admissionregistration/validation/static_suffix.go
// because that package cannot import from staging. Keep both in sync.
const StaticConfigSuffix = ".static.k8s.io"

// SplitYAMLDocuments splits a multi-document YAML byte slice into individual documents.
// Empty documents are skipped.
func SplitYAMLDocuments(data []byte) ([][]byte, error) {
	reader := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	var docs [][]byte
	for {
		doc, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		doc = bytes.TrimSpace(doc)
		if len(doc) == 0 {
			continue
		}
		docs = append(docs, doc)
	}
	return docs, nil
}

// FileDoc holds a decoded YAML document and the file it came from.
type FileDoc struct {
	FilePath string
	Doc      []byte
}

// LoadFiles reads all YAML/JSON files from dir, splits multi-document YAML,
// and returns individual documents with their source file paths plus the
// concatenated raw data for change-detection hashing.
// Files are processed in alphabetical order for deterministic behavior.
func LoadFiles(dir string) ([]FileDoc, []byte, error) {
	if len(dir) == 0 {
		return nil, nil, fmt.Errorf("manifest directory path is empty")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read manifest directory %q: %w", dir, err)
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	var fileDocs []FileDoc
	var allData []byte

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" && ext != ".json" {
			continue
		}

		filePath := filepath.Join(dir, name)
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read file %q: %w", filePath, err)
		}
		if len(data) == 0 {
			continue
		}

		allData = append(allData, data...)

		docs, err := SplitYAMLDocuments(data)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to split YAML documents in file %q: %w", filePath, err)
		}
		for _, doc := range docs {
			fileDocs = append(fileDocs, FileDoc{FilePath: filePath, Doc: doc})
		}
	}

	return fileDocs, allData, nil
}
