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

// MutatingPolicyLoadFunc loads MutatingAdmissionPolicy manifests from a directory.
type MutatingPolicyLoadFunc func(dir string) ([]*admissionregistrationv1.MutatingAdmissionPolicy, []*admissionregistrationv1.MutatingAdmissionPolicyBinding, []byte, error)

// MutatingCompiler compiles a MutatingAdmissionPolicy into an Evaluator.
type MutatingCompiler[E generic.Evaluator] func(*admissionregistrationv1.MutatingAdmissionPolicy) E

// StaticMutatingPolicySource provides mutating policy configurations loaded from manifest files.
type StaticMutatingPolicySource[E generic.Evaluator] struct {
	manifestsDir   string
	apiServerID    string
	reloadInterval time.Duration
	compiler       MutatingCompiler[E]
	loadFunc       MutatingPolicyLoadFunc

	current          atomic.Pointer[[]generic.PolicyHook[*admissionregistrationv1.MutatingAdmissionPolicy, *admissionregistrationv1.MutatingAdmissionPolicyBinding, E]]
	lastReadDataLock sync.Mutex
	lastReadData     []byte
	hasSynced        atomic.Bool
}

// NewStaticMutatingPolicySource creates a new source for mutating policy manifests.
func NewStaticMutatingPolicySource[E generic.Evaluator](manifestsDir, apiServerID string, compiler MutatingCompiler[E], loadFunc MutatingPolicyLoadFunc) *StaticMutatingPolicySource[E] {
	metrics.RegisterMetrics()
	return &StaticMutatingPolicySource[E]{
		manifestsDir:   manifestsDir,
		apiServerID:    apiServerID,
		reloadInterval: DefaultReloadInterval,
		compiler:       compiler,
		loadFunc:       loadFunc,
	}
}

// LoadInitial performs the initial load of manifests.
func (s *StaticMutatingPolicySource[E]) LoadInitial() error {
	hooks, rawData, err := s.loadAndCompile()
	if err != nil {
		return err
	}

	s.current.Store(&hooks)
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()
	s.hasSynced.Store(true)

	klog.InfoS("Loaded manifest-based mutating admission policy configurations", "count", len(hooks))
	metrics.RecordAutomaticReloadSuccess(metrics.MAPManifestType, s.apiServerID, string(rawData))
	return nil
}

// Run starts the file watcher and blocks until ctx is canceled.
func (s *StaticMutatingPolicySource[E]) Run(ctx context.Context) error {
	// Record initial config info
	s.lastReadDataLock.Lock()
	lastData := s.lastReadData
	s.lastReadDataLock.Unlock()
	if lastData != nil {
		metrics.RecordLastConfigInfo(metrics.MAPManifestType, s.apiServerID, string(lastData))
	}

	filesystem.WatchUntil(
		ctx,
		s.reloadInterval,
		s.manifestsDir,
		func() {
			s.checkAndReload()
		},
		func(err error) {
			klog.ErrorS(err, "watching MAP manifest directory", "dir", s.manifestsDir)
		},
	)
	return ctx.Err()
}

func (s *StaticMutatingPolicySource[E]) loadAndCompile() ([]generic.PolicyHook[*admissionregistrationv1.MutatingAdmissionPolicy, *admissionregistrationv1.MutatingAdmissionPolicyBinding, E], []byte, error) {
	policies, bindings, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		return nil, nil, err
	}

	// Pair policies with their bindings
	bindingsByPolicy := make(map[string][]*admissionregistrationv1.MutatingAdmissionPolicyBinding)
	for _, binding := range bindings {
		bindingsByPolicy[binding.Spec.PolicyName] = append(bindingsByPolicy[binding.Spec.PolicyName], binding)
	}

	var hooks []generic.PolicyHook[*admissionregistrationv1.MutatingAdmissionPolicy, *admissionregistrationv1.MutatingAdmissionPolicyBinding, E]
	for _, policy := range policies {
		evaluator := s.compiler(policy)
		hook := generic.PolicyHook[*admissionregistrationv1.MutatingAdmissionPolicy, *admissionregistrationv1.MutatingAdmissionPolicyBinding, E]{
			Policy:    policy,
			Bindings:  bindingsByPolicy[policy.Name],
			Evaluator: evaluator,
		}
		hooks = append(hooks, hook)
	}
	return hooks, rawData, nil
}

func (s *StaticMutatingPolicySource[E]) checkAndReload() {
	hooks, rawData, err := s.loadAndCompile()
	if err != nil {
		klog.ErrorS(err, "reloading MAP manifest config", "dir", s.manifestsDir)
		metrics.RecordAutomaticReloadFailure(metrics.MAPManifestType, s.apiServerID)
		return
	}

	s.lastReadDataLock.Lock()
	unchanged := bytes.Equal(rawData, s.lastReadData)
	s.lastReadDataLock.Unlock()

	if unchanged {
		return
	}

	klog.InfoS("found new MAP manifest config", "dir", s.manifestsDir)

	s.current.Store(&hooks)
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()

	klog.InfoS("reloaded MAP manifest config", "dir", s.manifestsDir)
	metrics.RecordAutomaticReloadSuccess(metrics.MAPManifestType, s.apiServerID, string(rawData))
}

// HasSynced returns true if the initial load has completed.
func (s *StaticMutatingPolicySource[E]) HasSynced() bool {
	return s.hasSynced.Load()
}

// Hooks returns the list of compiled policy hooks.
func (s *StaticMutatingPolicySource[E]) Hooks() []generic.PolicyHook[*admissionregistrationv1.MutatingAdmissionPolicy, *admissionregistrationv1.MutatingAdmissionPolicyBinding, E] {
	current := s.current.Load()
	if current == nil {
		return nil
	}
	return *current
}
