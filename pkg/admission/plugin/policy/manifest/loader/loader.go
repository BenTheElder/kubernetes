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

// Package loader provides functionality to load VAP/MAP configurations from
// manifest files with scheme-based defaulting and validation.
package loader

import (
	"fmt"
	"strings"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apiserver/pkg/admission/plugin/manifest"
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

// LoadedPolicyManifests holds the VAP configurations loaded from manifest files.
type LoadedPolicyManifests struct {
	// Policies is the list of loaded ValidatingAdmissionPolicy resources.
	Policies []*admissionregistrationv1.ValidatingAdmissionPolicy
	// Bindings is the list of loaded ValidatingAdmissionPolicyBinding resources.
	Bindings []*admissionregistrationv1.ValidatingAdmissionPolicyBinding
	// RawData is the concatenated raw data of all loaded files, used for change detection.
	RawData []byte
}

// PolicyWithBindings pairs a policy with its bindings.
type PolicyWithBindings struct {
	Policy   *admissionregistrationv1.ValidatingAdmissionPolicy
	Bindings []*admissionregistrationv1.ValidatingAdmissionPolicyBinding
}

// LoadManifestsFromDirectory reads all YAML and JSON files from the specified directory
// and parses them as ValidatingAdmissionPolicy or ValidatingAdmissionPolicyBinding resources.
// Files are processed in alphabetical order for deterministic behavior.
func LoadManifestsFromDirectory(dir string) (*LoadedPolicyManifests, error) {
	fileDocs, rawData, err := manifest.LoadFiles(dir)
	if err != nil {
		return nil, err
	}

	result := &LoadedPolicyManifests{RawData: rawData}
	seenPolicyNames := map[string]string{}
	seenBindingNames := map[string]string{}

	for _, fd := range fileDocs {
		obj, gvk, err := codecs.UniversalDeserializer().Decode(fd.Doc, nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to decode document in file %q: %w", fd.FilePath, err)
		}

		switch config := obj.(type) {
		case *admissionregistrationv1.ValidatingAdmissionPolicy:
			if err := defaultAndValidateVAP(config, fd.FilePath); err != nil {
				return nil, err
			}
			if err := validateManifestPolicy(config, fd.FilePath, seenPolicyNames); err != nil {
				return nil, err
			}
			result.Policies = append(result.Policies, config)
		case *admissionregistrationv1.ValidatingAdmissionPolicyBinding:
			if err := defaultAndValidateVAPB(config, fd.FilePath); err != nil {
				return nil, err
			}
			if err := validateManifestBinding(config, fd.FilePath, seenBindingNames); err != nil {
				return nil, err
			}
			result.Bindings = append(result.Bindings, config)
		case *admissionregistrationv1.ValidatingAdmissionPolicyList:
			for i := range config.Items {
				item := &config.Items[i]
				if err := defaultAndValidateVAP(item, fd.FilePath); err != nil {
					return nil, err
				}
				if err := validateManifestPolicy(item, fd.FilePath, seenPolicyNames); err != nil {
					return nil, err
				}
				result.Policies = append(result.Policies, item)
			}
		case *admissionregistrationv1.ValidatingAdmissionPolicyBindingList:
			for i := range config.Items {
				item := &config.Items[i]
				if err := defaultAndValidateVAPB(item, fd.FilePath); err != nil {
					return nil, err
				}
				if err := validateManifestBinding(item, fd.FilePath, seenBindingNames); err != nil {
					return nil, err
				}
				result.Bindings = append(result.Bindings, item)
			}
		case *metav1.List:
			for _, rawItem := range config.Items {
				itemObj, itemGVK, err := codecs.UniversalDeserializer().Decode(rawItem.Raw, nil, nil)
				if err != nil {
					return nil, fmt.Errorf("failed to decode list item in file %q: %w", fd.FilePath, err)
				}
				switch itemConfig := itemObj.(type) {
				case *admissionregistrationv1.ValidatingAdmissionPolicy:
					if err := defaultAndValidateVAP(itemConfig, fd.FilePath); err != nil {
						return nil, err
					}
					if err := validateManifestPolicy(itemConfig, fd.FilePath, seenPolicyNames); err != nil {
						return nil, err
					}
					result.Policies = append(result.Policies, itemConfig)
				case *admissionregistrationv1.ValidatingAdmissionPolicyBinding:
					if err := defaultAndValidateVAPB(itemConfig, fd.FilePath); err != nil {
						return nil, err
					}
					if err := validateManifestBinding(itemConfig, fd.FilePath, seenBindingNames); err != nil {
						return nil, err
					}
					result.Bindings = append(result.Bindings, itemConfig)
				default:
					return nil, fmt.Errorf("file %q contains unsupported resource type %v in List; only ValidatingAdmissionPolicy and ValidatingAdmissionPolicyBinding are supported", fd.FilePath, itemGVK)
				}
			}
		default:
			return nil, fmt.Errorf("file %q contains unsupported resource type %v; only ValidatingAdmissionPolicy, ValidatingAdmissionPolicyBinding, and their List variants are supported", fd.FilePath, gvk)
		}
	}

	if len(result.Policies) == 0 && len(result.Bindings) == 0 {
		klog.InfoS("No policy configurations found in manifest directory", "dir", dir)
	}

	if err := validateBindingReferences(result); err != nil {
		return nil, err
	}

	return result, nil
}

// defaultAndValidateVAP applies scheme defaults and standard validation.
func defaultAndValidateVAP(policy *admissionregistrationv1.ValidatingAdmissionPolicy, filePath string) error {
	scheme.Default(policy)

	internalObj := &admissionregistration.ValidatingAdmissionPolicy{}
	if err := scheme.Convert(policy, internalObj, nil); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicy %q in %q: conversion error: %w", policy.Name, filePath, err)
	}
	if errs := validation.ValidateValidatingAdmissionPolicy(internalObj); len(errs) > 0 {
		return fmt.Errorf("ValidatingAdmissionPolicy %q in %q: %w", policy.Name, filePath, errs.ToAggregate())
	}
	resultObj := &admissionregistrationv1.ValidatingAdmissionPolicy{}
	if err := scheme.Convert(internalObj, resultObj, nil); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicy %q in %q: back-conversion error: %w", policy.Name, filePath, err)
	}
	*policy = *resultObj
	return nil
}

// defaultAndValidateVAPB applies scheme defaults and standard validation.
func defaultAndValidateVAPB(binding *admissionregistrationv1.ValidatingAdmissionPolicyBinding, filePath string) error {
	scheme.Default(binding)

	internalObj := &admissionregistration.ValidatingAdmissionPolicyBinding{}
	if err := scheme.Convert(binding, internalObj, nil); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in %q: conversion error: %w", binding.Name, filePath, err)
	}
	if errs := validation.ValidateValidatingAdmissionPolicyBinding(internalObj); len(errs) > 0 {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in %q: %w", binding.Name, filePath, errs.ToAggregate())
	}
	resultObj := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{}
	if err := scheme.Convert(internalObj, resultObj, nil); err != nil {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in %q: back-conversion error: %w", binding.Name, filePath, err)
	}
	*binding = *resultObj
	return nil
}

// validateManifestPolicy validates manifest-specific constraints for a ValidatingAdmissionPolicy.
func validateManifestPolicy(policy *admissionregistrationv1.ValidatingAdmissionPolicy, filePath string, seenNames map[string]string) error {
	name := policy.Name
	if len(name) == 0 {
		return fmt.Errorf("ValidatingAdmissionPolicy in file %q must have a name", filePath)
	}
	if !strings.HasSuffix(name, staticConfigSuffix) {
		return fmt.Errorf("ValidatingAdmissionPolicy %q in file %q must have a name ending with %q", name, filePath, staticConfigSuffix)
	}
	if prevFile, ok := seenNames[name]; ok {
		return fmt.Errorf("duplicate ValidatingAdmissionPolicy name %q found in file %q (previously seen in %q)", name, filePath, prevFile)
	}
	seenNames[name] = filePath

	if policy.Spec.ParamKind != nil {
		return fmt.Errorf("ValidatingAdmissionPolicy %q in file %q: spec.paramKind is not supported for static manifests", name, filePath)
	}
	return nil
}

// validateManifestBinding validates manifest-specific constraints for a ValidatingAdmissionPolicyBinding.
func validateManifestBinding(binding *admissionregistrationv1.ValidatingAdmissionPolicyBinding, filePath string, seenNames map[string]string) error {
	name := binding.Name
	if len(name) == 0 {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding in file %q must have a name", filePath)
	}
	if !strings.HasSuffix(name, staticConfigSuffix) {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in file %q must have a name ending with %q", name, filePath, staticConfigSuffix)
	}
	if prevFile, ok := seenNames[name]; ok {
		return fmt.Errorf("duplicate ValidatingAdmissionPolicyBinding name %q found in file %q (previously seen in %q)", name, filePath, prevFile)
	}
	seenNames[name] = filePath

	if len(binding.Spec.PolicyName) == 0 {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in file %q must reference a policy (spec.policyName)", name, filePath)
	}
	if !strings.HasSuffix(binding.Spec.PolicyName, staticConfigSuffix) {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in file %q: spec.policyName %q must end with %q", name, filePath, binding.Spec.PolicyName, staticConfigSuffix)
	}
	if binding.Spec.ParamRef != nil {
		return fmt.Errorf("ValidatingAdmissionPolicyBinding %q in file %q: spec.paramRef is not supported for static manifests", name, filePath)
	}
	return nil
}

// validateBindingReferences ensures all bindings reference policies that exist in the manifest set.
func validateBindingReferences(manifests *LoadedPolicyManifests) error {
	policyNames := make(map[string]bool)
	for _, policy := range manifests.Policies {
		policyNames[policy.Name] = true
	}

	for _, binding := range manifests.Bindings {
		if !policyNames[binding.Spec.PolicyName] {
			return fmt.Errorf("ValidatingAdmissionPolicyBinding %q references policy %q which does not exist in the manifest directory", binding.Name, binding.Spec.PolicyName)
		}
	}
	return nil
}

// GetPoliciesWithBindings returns policies paired with their bindings.
func (l *LoadedPolicyManifests) GetPoliciesWithBindings() []PolicyWithBindings {
	bindingsByPolicy := make(map[string][]*admissionregistrationv1.ValidatingAdmissionPolicyBinding)
	for _, binding := range l.Bindings {
		bindingsByPolicy[binding.Spec.PolicyName] = append(bindingsByPolicy[binding.Spec.PolicyName], binding)
	}

	var result []PolicyWithBindings
	for _, policy := range l.Policies {
		pwb := PolicyWithBindings{
			Policy:   policy,
			Bindings: bindingsByPolicy[policy.Name],
		}
		result = append(result, pwb)
	}
	return result
}
