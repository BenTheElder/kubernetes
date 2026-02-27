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

package generic

import (
	"k8s.io/apiserver/pkg/admission/plugin/webhook"
)

// compositeWebhookSource combines multiple webhook sources into a single source.
// Static (manifest-based) webhooks are returned before API-based webhooks.
type compositeWebhookSource struct {
	staticSource Source
	apiSource    Source
}

var _ Source = &compositeWebhookSource{}

// NewCompositeWebhookSource creates a webhook source that combines static and API-based sources.
// Static webhooks are evaluated first, followed by API-based webhooks.
// If staticSource is nil, only apiSource webhooks are returned.
func NewCompositeWebhookSource(staticSource, apiSource Source) Source {
	if staticSource == nil {
		return apiSource
	}
	return &compositeWebhookSource{
		staticSource: staticSource,
		apiSource:    apiSource,
	}
}

// Webhooks returns all webhook accessors from both sources.
// Static webhooks come first, followed by API-based webhooks.
func (c *compositeWebhookSource) Webhooks() []webhook.WebhookAccessor {
	var result []webhook.WebhookAccessor

	// Static webhooks first (platform policies take precedence)
	if c.staticSource != nil {
		result = append(result, c.staticSource.Webhooks()...)
	}

	// Then API-based webhooks
	if c.apiSource != nil {
		result = append(result, c.apiSource.Webhooks()...)
	}

	return result
}

// HasSynced returns true only when both sources have synced.
func (c *compositeWebhookSource) HasSynced() bool {
	staticSynced := c.staticSource == nil || c.staticSource.HasSynced()
	apiSynced := c.apiSource == nil || c.apiSource.HasSynced()
	return staticSynced && apiSynced
}
