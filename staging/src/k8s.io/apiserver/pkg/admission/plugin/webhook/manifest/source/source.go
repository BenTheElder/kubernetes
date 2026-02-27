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

// Package source provides a Source implementation that loads webhook configurations from manifest files.
package source

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiserver/pkg/admission/plugin/webhook"
	"k8s.io/apiserver/pkg/admission/plugin/webhook/manifest/metrics"
	"k8s.io/apiserver/pkg/util/filesystem"
	"k8s.io/klog/v2"
)

const (
	// DefaultReloadInterval is the default interval at which the manifest directory is checked for changes.
	DefaultReloadInterval = 1 * time.Minute
)

// ValidatingWebhookLoadFunc loads validating webhook configurations from a directory.
// Returns the configurations, raw data for change detection, and any error.
type ValidatingWebhookLoadFunc func(dir string) ([]*admissionregistrationv1.ValidatingWebhookConfiguration, []byte, error)

// MutatingWebhookLoadFunc loads mutating webhook configurations from a directory.
// Returns the configurations, raw data for change detection, and any error.
type MutatingWebhookLoadFunc func(dir string) ([]*admissionregistrationv1.MutatingWebhookConfiguration, []byte, error)

// validatingData holds the currently loaded validating webhook configurations.
type validatingData struct {
	configurations []*admissionregistrationv1.ValidatingWebhookConfiguration
	rawData        []byte
}

// mutatingData holds the currently loaded mutating webhook configurations.
type mutatingData struct {
	configurations []*admissionregistrationv1.MutatingWebhookConfiguration
	rawData        []byte
}

// ValidatingSource provides validating webhook configurations loaded from manifest files.
type ValidatingSource struct {
	manifestsDir   string
	apiServerID    string
	reloadInterval time.Duration
	loadFunc       ValidatingWebhookLoadFunc

	current          atomic.Pointer[validatingData]
	lastReadDataLock sync.Mutex
	lastReadData     []byte
	hasSynced        atomic.Bool
}

// MutatingSource provides mutating webhook configurations loaded from manifest files.
type MutatingSource struct {
	manifestsDir   string
	apiServerID    string
	reloadInterval time.Duration
	loadFunc       MutatingWebhookLoadFunc

	current          atomic.Pointer[mutatingData]
	lastReadDataLock sync.Mutex
	lastReadData     []byte
	hasSynced        atomic.Bool
}

// NewValidatingSource creates a new validating webhook source that loads configurations from the specified directory.
func NewValidatingSource(manifestsDir, apiServerID string, loadFunc ValidatingWebhookLoadFunc) *ValidatingSource {
	metrics.RegisterMetrics()
	return &ValidatingSource{
		manifestsDir:   manifestsDir,
		apiServerID:    apiServerID,
		reloadInterval: DefaultReloadInterval,
		loadFunc:       loadFunc,
	}
}

// NewMutatingSource creates a new mutating webhook source that loads configurations from the specified directory.
func NewMutatingSource(manifestsDir, apiServerID string, loadFunc MutatingWebhookLoadFunc) *MutatingSource {
	metrics.RegisterMetrics()
	return &MutatingSource{
		manifestsDir:   manifestsDir,
		apiServerID:    apiServerID,
		reloadInterval: DefaultReloadInterval,
		loadFunc:       loadFunc,
	}
}

// LoadInitial performs the initial load of validating webhook manifests.
func (s *ValidatingSource) LoadInitial() error {
	configs, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		return err
	}

	s.current.Store(&validatingData{configurations: configs, rawData: rawData})
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()
	s.hasSynced.Store(true)

	klog.InfoS("Loaded manifest-based webhook configurations", "plugin", string(metrics.ValidatingWebhookManifestType),
		"validatingWebhookConfigurations", len(configs))
	metrics.RecordAutomaticReloadSuccess(metrics.ValidatingWebhookManifestType, s.apiServerID, string(rawData))
	return nil
}

// LoadInitial performs the initial load of mutating webhook manifests.
func (s *MutatingSource) LoadInitial() error {
	configs, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		return err
	}

	s.current.Store(&mutatingData{configurations: configs, rawData: rawData})
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()
	s.hasSynced.Store(true)

	klog.InfoS("Loaded manifest-based webhook configurations", "plugin", string(metrics.MutatingWebhookManifestType),
		"mutatingWebhookConfigurations", len(configs))
	metrics.RecordAutomaticReloadSuccess(metrics.MutatingWebhookManifestType, s.apiServerID, string(rawData))
	return nil
}

// Run starts the file watcher and blocks until ctx is canceled.
func (s *ValidatingSource) Run(ctx context.Context) {
	if current := s.current.Load(); current != nil {
		metrics.RecordLastConfigInfo(metrics.ValidatingWebhookManifestType, s.apiServerID, string(current.rawData))
	}

	filesystem.WatchUntil(
		ctx,
		s.reloadInterval,
		s.manifestsDir,
		func() {
			s.checkAndReload()
		},
		func(err error) {
			klog.ErrorS(err, "watching manifest directory", "dir", s.manifestsDir)
		},
	)
}

// Run starts the file watcher and blocks until ctx is canceled.
func (s *MutatingSource) Run(ctx context.Context) {
	if current := s.current.Load(); current != nil {
		metrics.RecordLastConfigInfo(metrics.MutatingWebhookManifestType, s.apiServerID, string(current.rawData))
	}

	filesystem.WatchUntil(
		ctx,
		s.reloadInterval,
		s.manifestsDir,
		func() {
			s.checkAndReload()
		},
		func(err error) {
			klog.ErrorS(err, "watching manifest directory", "dir", s.manifestsDir)
		},
	)
}

func (s *ValidatingSource) checkAndReload() {
	configs, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		klog.ErrorS(err, "reloading admission manifest config", "dir", s.manifestsDir)
		metrics.RecordAutomaticReloadFailure(metrics.ValidatingWebhookManifestType, s.apiServerID)
		return
	}

	s.lastReadDataLock.Lock()
	unchanged := bytes.Equal(rawData, s.lastReadData)
	s.lastReadDataLock.Unlock()

	if unchanged {
		return
	}

	klog.InfoS("found new admission manifest config", "dir", s.manifestsDir)

	s.current.Store(&validatingData{configurations: configs, rawData: rawData})
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()

	klog.InfoS("reloaded admission manifest config", "dir", s.manifestsDir)
	metrics.RecordAutomaticReloadSuccess(metrics.ValidatingWebhookManifestType, s.apiServerID, string(rawData))
}

func (s *MutatingSource) checkAndReload() {
	configs, rawData, err := s.loadFunc(s.manifestsDir)
	if err != nil {
		klog.ErrorS(err, "reloading admission manifest config", "dir", s.manifestsDir)
		metrics.RecordAutomaticReloadFailure(metrics.MutatingWebhookManifestType, s.apiServerID)
		return
	}

	s.lastReadDataLock.Lock()
	unchanged := bytes.Equal(rawData, s.lastReadData)
	s.lastReadDataLock.Unlock()

	if unchanged {
		return
	}

	klog.InfoS("found new admission manifest config", "dir", s.manifestsDir)

	s.current.Store(&mutatingData{configurations: configs, rawData: rawData})
	s.lastReadDataLock.Lock()
	s.lastReadData = rawData
	s.lastReadDataLock.Unlock()

	klog.InfoS("reloaded admission manifest config", "dir", s.manifestsDir)
	metrics.RecordAutomaticReloadSuccess(metrics.MutatingWebhookManifestType, s.apiServerID, string(rawData))
}

// HasSynced returns true if the initial load has completed.
func (s *ValidatingSource) HasSynced() bool {
	return s.hasSynced.Load()
}

// HasSynced returns true if the initial load has completed.
func (s *MutatingSource) HasSynced() bool {
	return s.hasSynced.Load()
}

// Webhooks returns the list of validating webhook accessors.
func (s *ValidatingSource) Webhooks() []webhook.WebhookAccessor {
	current := s.current.Load()
	if current == nil {
		return nil
	}
	var accessors []webhook.WebhookAccessor
	for _, config := range current.configurations {
		names := map[string]int{}
		for i := range config.Webhooks {
			w := &config.Webhooks[i]
			n := w.Name
			uid := "manifest/" + config.Name + "/" + n + "/" + fmt.Sprintf("%d", names[n])
			names[n]++
			accessors = append(accessors, webhook.NewValidatingWebhookAccessor(uid, config.Name, w))
		}
	}
	return accessors
}

// Webhooks returns the list of mutating webhook accessors.
func (s *MutatingSource) Webhooks() []webhook.WebhookAccessor {
	current := s.current.Load()
	if current == nil {
		return nil
	}
	var accessors []webhook.WebhookAccessor
	for _, config := range current.configurations {
		names := map[string]int{}
		for i := range config.Webhooks {
			w := &config.Webhooks[i]
			n := w.Name
			uid := "manifest/" + config.Name + "/" + n + "/" + fmt.Sprintf("%d", names[n])
			names[n]++
			accessors = append(accessors, webhook.NewMutatingWebhookAccessor(uid, config.Name, w))
		}
	}
	return accessors
}
