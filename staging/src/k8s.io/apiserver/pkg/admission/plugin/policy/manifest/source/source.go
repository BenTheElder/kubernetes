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

// Package source provides a Source implementation that loads VAP configurations from manifest files.
package source

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiserver/pkg/admission/plugin/policy/generic"
	"k8s.io/apiserver/pkg/admission/plugin/webhook/manifest/metrics"
	"k8s.io/apiserver/pkg/util/filesystem"
	"k8s.io/klog/v2"
)

const (
	// DefaultReloadInterval is the default interval at which the manifest directory is checked for changes.
	DefaultReloadInterval = 1 * time.Minute
)

// PolicyLoadFunc loads ValidatingAdmissionPolicy manifests from a directory.
type PolicyLoadFunc func(dir string) ([]*admissionregistrationv1.ValidatingAdmissionPolicy, []*admissionregistrationv1.ValidatingAdmissionPolicyBinding, []byte, error)

// Compiler compiles a ValidatingAdmissionPolicy into an Evaluator.
type Compiler[E generic.Evaluator] func(*admissionregistrationv1.ValidatingAdmissionPolicy) E

// StaticPolicySource provides policy configurations loaded from manifest files.
type StaticPolicySource[E generic.Evaluator] struct {
	// manifestsDir is the absolute path to the directory containing policy manifest files.
	manifestsDir string

	// apiServerID identifies this API server instance for metrics.
	apiServerID string

	// reloadInterval is how often to check for file changes.
	reloadInterval time.Duration

	// compiler compiles policies into evaluators.
	compiler Compiler[E]

	// loadFunc loads policy manifests from the directory.
	loadFunc PolicyLoadFunc

	// current holds the currently loaded and compiled policy hooks.
	current atomic.Pointer[[]generic.PolicyHook[*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding, E]]

	// lastReadData holds the raw data from the last successful read for change detection.
	lastReadDataLock sync.Mutex
	lastReadData     []byte

	// hasSynced indicates whether the initial load has completed.
	hasSynced atomic.Bool
}

// NewStaticPolicySource creates a new static policy source that loads configurations from the specified directory.
func NewStaticPolicySource[E generic.Evaluator](manifestsDir, apiServerID string, compiler Compiler[E], loadFunc PolicyLoadFunc) *StaticPolicySource[E] {
	metrics.RegisterMetrics()
	return &StaticPolicySource[E]{
		manifestsDir:   manifestsDir,
		apiServerID:    apiServerID,
		reloadInterval: DefaultReloadInterval,
		compiler:       compiler,
		loadFunc:       loadFunc,
	}
}

// LoadInitial performs the initial load of manifests.
// This should be called during API server startup and will fail if the manifests cannot be loaded.
func (s *StaticPolicySource[E]) LoadInitial() error {
	hooks, rawData, err := s.loadAndCompile()
	if err != nil {
		return err
	}

	s.current.Store(&hooks)
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()
	s.hasSynced.Store(true)

	klog.InfoS("Loaded manifest-based admission policy configurations", "count", len(hooks))
	metrics.RecordAutomaticReloadSuccess(metrics.VAPManifestType, s.apiServerID, string(rawData))
	return nil
}

// Run starts the file watcher and blocks until ctx is canceled.
func (s *StaticPolicySource[E]) Run(ctx context.Context) error {
	// Record initial config info
	s.lastReadDataLock.Lock()
	lastData := s.lastReadData
	s.lastReadDataLock.Unlock()
	if lastData != nil {
		metrics.RecordLastConfigInfo(metrics.VAPManifestType, s.apiServerID, string(lastData))
	}

	filesystem.WatchUntil(
		ctx,
		s.reloadInterval,
		s.manifestsDir,
		func() {
			s.checkAndReload()
		},
		func(err error) {
			klog.ErrorS(err, "watching VAP manifest directory", "dir", s.manifestsDir)
		},
	)
	return ctx.Err()
}

func (s *StaticPolicySource[E]) loadAndCompile() ([]generic.PolicyHook[*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding, E], []byte, error) {
	policies, bindings, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		return nil, nil, err
	}

	// Pair policies with their bindings
	bindingsByPolicy := make(map[string][]*admissionregistrationv1.ValidatingAdmissionPolicyBinding)
	for _, binding := range bindings {
		bindingsByPolicy[binding.Spec.PolicyName] = append(bindingsByPolicy[binding.Spec.PolicyName], binding)
	}

	var hooks []generic.PolicyHook[*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding, E]

	for _, policy := range policies {
		evaluator := s.compiler(policy)
		hook := generic.PolicyHook[*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding, E]{
			Policy:    policy,
			Bindings:  bindingsByPolicy[policy.Name],
			Evaluator: evaluator,
		}
		hooks = append(hooks, hook)
	}

	return hooks, rawData, nil
}

func (s *StaticPolicySource[E]) checkAndReload() {
	hooks, rawData, err := s.loadAndCompile()
	if err != nil {
		klog.ErrorS(err, "reloading VAP manifest config", "dir", s.manifestsDir)
		metrics.RecordAutomaticReloadFailure(metrics.VAPManifestType, s.apiServerID)
		return
	}

	s.lastReadDataLock.Lock()
	unchanged := bytes.Equal(rawData, s.lastReadData)
	s.lastReadDataLock.Unlock()

	if unchanged {
		// No change detected
		return
	}

	klog.InfoS("found new VAP manifest config", "dir", s.manifestsDir)

	// Update the current config
	s.current.Store(&hooks)
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()

	klog.InfoS("reloaded VAP manifest config", "dir", s.manifestsDir)
	metrics.RecordAutomaticReloadSuccess(metrics.VAPManifestType, s.apiServerID, string(rawData))
}

// HasSynced returns true if the initial load has completed.
func (s *StaticPolicySource[E]) HasSynced() bool {
	return s.hasSynced.Load()
}

// Hooks returns the list of policy hooks.
func (s *StaticPolicySource[E]) Hooks() []generic.PolicyHook[*admissionregistrationv1.ValidatingAdmissionPolicy, *admissionregistrationv1.ValidatingAdmissionPolicyBinding, E] {
	current := s.current.Load()
	if current == nil {
		return nil
	}
	return *current
}
