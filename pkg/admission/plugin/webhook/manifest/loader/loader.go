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

// Package loader provides functionality to load webhook configurations from
// manifest files with scheme-based defaulting and validation.
package loader

import (
	"fmt"
	"sort"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/admission/plugin/manifest"
	"k8s.io/apiserver/pkg/admission/plugin/webhook"
	"k8s.io/klog/v2"
	admissionregistration "k8s.io/kubernetes/pkg/apis/admissionregistration"
	admissionregistrationinstall "k8s.io/kubernetes/pkg/apis/admissionregistration/install"
	"k8s.io/kubernetes/pkg/apis/admissionregistration/validation"
)

const staticConfigSuffix = manifest.StaticConfigSuffix

var (
	scheme = runtime.NewScheme()
	codecs = serializer.NewCodecFactory(scheme, serializer.EnableStrict)
)

func init() {
	admissionregistrationinstall.Install(scheme)
	scheme.AddUnversionedTypes(metav1.SchemeGroupVersion, &metav1.List{}, &metav1.Status{})
}

// ValidatingLoadResult holds the validating webhook configurations loaded from manifest files.
type ValidatingLoadResult struct {
	// Configurations is the list of loaded validating webhook configurations.
	Configurations []*admissionregistrationv1.ValidatingWebhookConfiguration
	// RawData is the concatenated raw data of all loaded files, used for hashing.
	RawData []byte
}

// MutatingLoadResult holds the mutating webhook configurations loaded from manifest files.
type MutatingLoadResult struct {
	// Configurations is the list of loaded mutating webhook configurations.
	Configurations []*admissionregistrationv1.MutatingWebhookConfiguration
	// RawData is the concatenated raw data of all loaded files, used for hashing.
	RawData []byte
}

// LoadValidatingManifests reads all YAML and JSON files from the specified directory
// and parses them as ValidatingWebhookConfiguration resources.
// Files containing MutatingWebhookConfiguration cause an error.
// Files are processed in alphabetical order for deterministic behavior.
func LoadValidatingManifests(dir string) (*ValidatingLoadResult, error) {
	fileDocs, rawData, err := manifest.LoadFiles(dir)
	if err != nil {
		return nil, err
	}

	result := &ValidatingLoadResult{RawData: rawData}
	seenNames := map[string]string{}

	for _, fd := range fileDocs {
		obj, gvk, err := codecs.UniversalDeserializer().Decode(fd.Doc, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decode document in file %q: %w", fd.FilePath, err)
		}

		switch config := obj.(type) {
		case *admissionregistrationv1.ValidatingWebhookConfiguration:
			if err := processValidatingWebhook(config, fd.FilePath, seenNames); err != nil {
				return nil, err
			}
			result.Configurations = append(result.Configurations, config)

		case *admissionregistrationv1.ValidatingWebhookConfigurationList:
			for i := range config.Items {
				if err := processValidatingWebhook(&config.Items[i], fd.FilePath, seenNames); err != nil {
					return nil, err
				}
				result.Configurations = append(result.Configurations, &config.Items[i])
			}

		case *metav1.List:
			for _, rawItem := range config.Items {
				itemObj, itemGVK, err := codecs.UniversalDeserializer().Decode(rawItem.Raw, nil, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to decode list item in file %q: %w", fd.FilePath, err)
				}
				vwc, ok := itemObj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
				if !ok {
					return nil, fmt.Errorf("file %q contains unsupported resource type %v in List; only ValidatingWebhookConfiguration is supported in this directory", fd.FilePath, itemGVK)
				}
				if err := processValidatingWebhook(vwc, fd.FilePath, seenNames); err != nil {
					return nil, fmt.Errorf("in List: %w", err)
				}
				result.Configurations = append(result.Configurations, vwc)
			}

		case *admissionregistrationv1.MutatingWebhookConfiguration,
			*admissionregistrationv1.MutatingWebhookConfigurationList:
			return nil, fmt.Errorf("file %q contains %v but this directory is configured for ValidatingAdmissionWebhook plugin; use a separate directory", fd.FilePath, gvk.Kind)

		default:
			return nil, fmt.Errorf("file %q contains unsupported resource type %v; only ValidatingWebhookConfiguration and its List variants are supported", fd.FilePath, gvk)
		}
	}

	if len(result.Configurations) == 0 {
		klog.InfoS("No webhook configurations found in manifest directory", "dir", dir)
	}

	sort.Slice(result.Configurations, func(i, j int) bool {
		return result.Configurations[i].Name < result.Configurations[j].Name
	})

	return result, nil
}

// LoadMutatingManifests reads all YAML and JSON files from the specified directory
// and parses them as MutatingWebhookConfiguration resources.
// Files containing ValidatingWebhookConfiguration cause an error.
// Files are processed in alphabetical order for deterministic behavior.
func LoadMutatingManifests(dir string) (*MutatingLoadResult, error) {
	fileDocs, rawData, err := manifest.LoadFiles(dir)
	if err != nil {
		return nil, err
	}

	result := &MutatingLoadResult{RawData: rawData}
	seenNames := map[string]string{}

	for _, fd := range fileDocs {
		obj, gvk, err := codecs.UniversalDeserializer().Decode(fd.Doc, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decode document in file %q: %w", fd.FilePath, err)
		}

		switch config := obj.(type) {
		case *admissionregistrationv1.MutatingWebhookConfiguration:
			if err := processMutatingWebhook(config, fd.FilePath, seenNames); err != nil {
				return nil, err
			}
			result.Configurations = append(result.Configurations, config)

		case *admissionregistrationv1.MutatingWebhookConfigurationList:
			for i := range config.Items {
				if err := processMutatingWebhook(&config.Items[i], fd.FilePath, seenNames); err != nil {
					return nil, err
				}
				result.Configurations = append(result.Configurations, &config.Items[i])
			}

		case *metav1.List:
			for _, rawItem := range config.Items {
				itemObj, itemGVK, err := codecs.UniversalDeserializer().Decode(rawItem.Raw, nil, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to decode list item in file %q: %w", fd.FilePath, err)
				}
				mwc, ok := itemObj.(*admissionregistrationv1.MutatingWebhookConfiguration)
				if !ok {
					return nil, fmt.Errorf("file %q contains unsupported resource type %v in List; only MutatingWebhookConfiguration is supported in this directory", fd.FilePath, itemGVK)
				}
				if err := processMutatingWebhook(mwc, fd.FilePath, seenNames); err != nil {
					return nil, fmt.Errorf("in List: %w", err)
				}
				result.Configurations = append(result.Configurations, mwc)
			}

		case *admissionregistrationv1.ValidatingWebhookConfiguration,
			*admissionregistrationv1.ValidatingWebhookConfigurationList:
			return nil, fmt.Errorf("file %q contains %v but this directory is configured for MutatingAdmissionWebhook plugin; use a separate directory", fd.FilePath, gvk.Kind)

		default:
			return nil, fmt.Errorf("file %q contains unsupported resource type %v; only MutatingWebhookConfiguration and its List variants are supported", fd.FilePath, gvk)
		}
	}

	if len(result.Configurations) == 0 {
		klog.InfoS("No webhook configurations found in manifest directory", "dir", dir)
	}

	sort.Slice(result.Configurations, func(i, j int) bool {
		return result.Configurations[i].Name < result.Configurations[j].Name
	})

	return result, nil
}

// processValidatingWebhook applies defaulting, standard validation, and manifest-specific
// validation to a ValidatingWebhookConfiguration.
func processValidatingWebhook(config *admissionregistrationv1.ValidatingWebhookConfiguration, filePath string, seenNames map[string]string) error {
	if err := defaultAndValidateVWC(config, filePath); err != nil {
		return err
	}
	return validateManifestVWC(config, filePath, seenNames)
}

// processMutatingWebhook applies defaulting, standard validation, and manifest-specific
// validation to a MutatingWebhookConfiguration.
func processMutatingWebhook(config *admissionregistrationv1.MutatingWebhookConfiguration, filePath string, seenNames map[string]string) error {
	if err := defaultAndValidateMWC(config, filePath); err != nil {
		return err
	}
	return validateManifestMWC(config, filePath, seenNames)
}

// defaultAndValidateVWC applies scheme defaults and runs standard validation
// on a ValidatingWebhookConfiguration.
func defaultAndValidateVWC(config *admissionregistrationv1.ValidatingWebhookConfiguration, filePath string) error {
	scheme.Default(config)

	internalObj := &admissionregistration.ValidatingWebhookConfiguration{}
	if err := scheme.Convert(config, internalObj, nil); err != nil {
		return fmt.Errorf("ValidatingWebhookConfiguration %q in %q: conversion error: %w", config.Name, filePath, err)
	}
	if errs := validation.ValidateValidatingWebhookConfiguration(internalObj); len(errs) > 0 {
		return fmt.Errorf("ValidatingWebhookConfiguration %q in %q: %w", config.Name, filePath, errs.ToAggregate())
	}
	// Convert back to get the fully defaulted and validated v1 object
	resultConfig := &admissionregistrationv1.ValidatingWebhookConfiguration{}
	if err := scheme.Convert(internalObj, resultConfig, nil); err != nil {
		return fmt.Errorf("ValidatingWebhookConfiguration %q in %q: back-conversion error: %w", config.Name, filePath, err)
	}
	*config = *resultConfig
	return nil
}

// defaultAndValidateMWC applies scheme defaults and runs standard validation
// on a MutatingWebhookConfiguration.
func defaultAndValidateMWC(config *admissionregistrationv1.MutatingWebhookConfiguration, filePath string) error {
	scheme.Default(config)

	internalObj := &admissionregistration.MutatingWebhookConfiguration{}
	if err := scheme.Convert(config, internalObj, nil); err != nil {
		return fmt.Errorf("MutatingWebhookConfiguration %q in %q: conversion error: %w", config.Name, filePath, err)
	}
	if errs := validation.ValidateMutatingWebhookConfiguration(internalObj); len(errs) > 0 {
		return fmt.Errorf("MutatingWebhookConfiguration %q in %q: %w", config.Name, filePath, errs.ToAggregate())
	}
	resultConfig := &admissionregistrationv1.MutatingWebhookConfiguration{}
	if err := scheme.Convert(internalObj, resultConfig, nil); err != nil {
		return fmt.Errorf("MutatingWebhookConfiguration %q in %q: back-conversion error: %w", config.Name, filePath, err)
	}
	*config = *resultConfig
	return nil
}

// validateManifestVWC runs manifest-specific validation on a ValidatingWebhookConfiguration.
// These are additional constraints beyond standard API validation.
func validateManifestVWC(config *admissionregistrationv1.ValidatingWebhookConfiguration, filePath string, seenNames map[string]string) error {
	if err := validateManifestName(config.Name, "ValidatingWebhookConfiguration", filePath, seenNames); err != nil {
		return err
	}
	for _, wh := range config.Webhooks {
		if err := validateWebhookClientConfig(wh.Name, config.Name, "ValidatingWebhookConfiguration", wh.ClientConfig, filePath); err != nil {
			return err
		}
	}
	return nil
}

// validateManifestMWC runs manifest-specific validation on a MutatingWebhookConfiguration.
func validateManifestMWC(config *admissionregistrationv1.MutatingWebhookConfiguration, filePath string, seenNames map[string]string) error {
	if err := validateManifestName(config.Name, "MutatingWebhookConfiguration", filePath, seenNames); err != nil {
		return err
	}
	for _, wh := range config.Webhooks {
		if err := validateWebhookClientConfig(wh.Name, config.Name, "MutatingWebhookConfiguration", wh.ClientConfig, filePath); err != nil {
			return err
		}
	}
	return nil
}

// validateManifestName checks that the object name is non-empty, has the required
// .static.k8s.io suffix, and is unique within its type.
func validateManifestName(name, kind, filePath string, seenNames map[string]string) error {
	if len(name) == 0 {
		return fmt.Errorf("%s in file %q must have a name", kind, filePath)
	}
	if !strings.HasSuffix(name, staticConfigSuffix) {
		return fmt.Errorf("%s %q in file %q must have a name ending with %q", kind, name, filePath, staticConfigSuffix)
	}
	if prevFile, ok := seenNames[name]; ok {
		return fmt.Errorf("duplicate %s name %q found in file %q (previously seen in %q)", kind, name, filePath, prevFile)
	}
	seenNames[name] = filePath
	return nil
}

// validateWebhookClientConfig checks that a webhook uses URL-based client config
// (service references are not supported for static manifests).
func validateWebhookClientConfig(webhookName, configName, kind string, cc admissionregistrationv1.WebhookClientConfig, filePath string) error {
	if cc.Service != nil {
		return fmt.Errorf("webhook %q in %s %q (file %q): clientConfig.service is not supported for static manifests; use clientConfig.url instead", webhookName, kind, configName, filePath)
	}
	if cc.URL == nil || len(*cc.URL) == 0 {
		return fmt.Errorf("webhook %q in %s %q (file %q): clientConfig.url is required for static manifests", webhookName, kind, configName, filePath)
	}
	if len(cc.CABundle) == 0 {
		return fmt.Errorf("webhook %q in %s %q (file %q): clientConfig.caBundle is required for static manifests", webhookName, kind, configName, filePath)
	}
	return nil
}

// GetWebhookAccessors returns webhook accessors for all validating webhooks.
func (r *ValidatingLoadResult) GetWebhookAccessors() []webhook.WebhookAccessor {
	var accessors []webhook.WebhookAccessor
	for _, config := range r.Configurations {
		names := map[string]int{}
		for i := range config.Webhooks {
			w := &config.Webhooks[i]
			n := w.Name
			uid := fmt.Sprintf("manifest/%s/%s/%d", config.Name, n, names[n])
			names[n]++
			accessor := webhook.NewValidatingWebhookAccessor(uid, config.Name, w)
			accessors = append(accessors, accessor)
		}
	}
	return accessors
}

// GetWebhookAccessors returns webhook accessors for all mutating webhooks.
func (r *MutatingLoadResult) GetWebhookAccessors() []webhook.WebhookAccessor {
	var accessors []webhook.WebhookAccessor
	for _, config := range r.Configurations {
		names := map[string]int{}
		for i := range config.Webhooks {
			w := &config.Webhooks[i]
			n := w.Name
			uid := fmt.Sprintf("manifest/%s/%s/%d", config.Name, n, names[n])
			names[n]++
			accessor := webhook.NewMutatingWebhookAccessor(uid, config.Name, w)
			accessors = append(accessors, accessor)
		}
	}
	return accessors
}
