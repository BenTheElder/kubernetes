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
	"context"
)

// compositePolicySource combines multiple policy sources into a single source.
// Static (manifest-based) policies are returned before API-based policies.
type compositePolicySource[H Hook] struct {
	staticSource Source[H]
	apiSource    Source[H]
}

var _ Source[Hook] = &compositePolicySource[Hook]{}

// NewCompositePolicySource creates a policy source that combines static and API-based sources.
// Static policies are evaluated first, followed by API-based policies.
// If staticSource is nil, only apiSource policies are returned.
func NewCompositePolicySource[H Hook](staticSource, apiSource Source[H]) Source[H] {
	if staticSource == nil {
		return apiSource
	}
	return &compositePolicySource[H]{
		staticSource: staticSource,
		apiSource:    apiSource,
	}
}

// Hooks returns all policy hooks from both sources.
// Static policies come first, followed by API-based policies.
func (c *compositePolicySource[H]) Hooks() []H {
	var result []H

	// Static policies first (platform policies take precedence)
	if c.staticSource != nil {
		result = append(result, c.staticSource.Hooks()...)
	}

	// Then API-based policies
	if c.apiSource != nil {
		result = append(result, c.apiSource.Hooks()...)
	}

	return result
}

// Run starts the API-based source. The static source is started separately
// by the plugin via its own goroutine (file watcher), so this method only
// needs to handle the API source lifecycle.
func (c *compositePolicySource[H]) Run(ctx context.Context) error {
	if c.apiSource != nil {
		return c.apiSource.Run(ctx)
	}
	return nil
}

// HasSynced returns true only when both sources have synced.
func (c *compositePolicySource[H]) HasSynced() bool {
	staticSynced := c.staticSource == nil || c.staticSource.HasSynced()
	apiSynced := c.apiSource == nil || c.apiSource.HasSynced()
	return staticSynced && apiSynced
}
