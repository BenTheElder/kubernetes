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
	"testing"

	v1 "k8s.io/api/admissionregistration/v1"
	"k8s.io/apiserver/pkg/admission/plugin/webhook"
	"k8s.io/utils/ptr"
)

// mockSource implements Source for testing.
type mockSource struct {
	webhooks  []webhook.WebhookAccessor
	hasSynced bool
}

func (m *mockSource) Webhooks() []webhook.WebhookAccessor {
	return m.webhooks
}

func (m *mockSource) HasSynced() bool {
	return m.hasSynced
}

var _ Source = &mockSource{}

func createTestValidatingWebhook(uid, name string) webhook.WebhookAccessor {
	return webhook.NewValidatingWebhookAccessor(uid, name, &v1.ValidatingWebhook{
		Name: "test.webhook.io",
		ClientConfig: v1.WebhookClientConfig{
			URL: ptr.To("https://example.com"),
		},
		AdmissionReviewVersions: []string{"v1"},
	})
}

func TestCompositeWebhookSource_Webhooks(t *testing.T) {
	staticWebhook := createTestValidatingWebhook("static-1", "static-config")
	apiWebhook := createTestValidatingWebhook("api-1", "api-config")

	tests := []struct {
		name         string
		staticSource Source
		apiSource    Source
		wantUIDs     []string
	}{
		{
			name:         "only static source",
			staticSource: &mockSource{webhooks: []webhook.WebhookAccessor{staticWebhook}, hasSynced: true},
			apiSource:    &mockSource{webhooks: nil, hasSynced: true},
			wantUIDs:     []string{"static-1"},
		},
		{
			name:         "only api source",
			staticSource: &mockSource{webhooks: nil, hasSynced: true},
			apiSource:    &mockSource{webhooks: []webhook.WebhookAccessor{apiWebhook}, hasSynced: true},
			wantUIDs:     []string{"api-1"},
		},
		{
			name:         "both sources - static first",
			staticSource: &mockSource{webhooks: []webhook.WebhookAccessor{staticWebhook}, hasSynced: true},
			apiSource:    &mockSource{webhooks: []webhook.WebhookAccessor{apiWebhook}, hasSynced: true},
			wantUIDs:     []string{"static-1", "api-1"},
		},
		{
			name:         "nil static source",
			staticSource: nil,
			apiSource:    &mockSource{webhooks: []webhook.WebhookAccessor{apiWebhook}, hasSynced: true},
			wantUIDs:     []string{"api-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewCompositeWebhookSource(tt.staticSource, tt.apiSource)
			webhooks := source.Webhooks()

			if len(webhooks) != len(tt.wantUIDs) {
				t.Errorf("Webhooks() returned %d webhooks, want %d", len(webhooks), len(tt.wantUIDs))
				return
			}

			for i, w := range webhooks {
				if w.GetUID() != tt.wantUIDs[i] {
					t.Errorf("Webhooks()[%d].GetUID() = %s, want %s", i, w.GetUID(), tt.wantUIDs[i])
				}
			}
		})
	}
}

func TestCompositeWebhookSource_HasSynced(t *testing.T) {
	tests := []struct {
		name         string
		staticSource Source
		apiSource    Source
		want         bool
	}{
		{
			name:         "both synced",
			staticSource: &mockSource{hasSynced: true},
			apiSource:    &mockSource{hasSynced: true},
			want:         true,
		},
		{
			name:         "static not synced",
			staticSource: &mockSource{hasSynced: false},
			apiSource:    &mockSource{hasSynced: true},
			want:         false,
		},
		{
			name:         "api not synced",
			staticSource: &mockSource{hasSynced: true},
			apiSource:    &mockSource{hasSynced: false},
			want:         false,
		},
		{
			name:         "neither synced",
			staticSource: &mockSource{hasSynced: false},
			apiSource:    &mockSource{hasSynced: false},
			want:         false,
		},
		{
			name:         "nil static source, api synced",
			staticSource: nil,
			apiSource:    &mockSource{hasSynced: true},
			want:         true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewCompositeWebhookSource(tt.staticSource, tt.apiSource)
			if got := source.HasSynced(); got != tt.want {
				t.Errorf("HasSynced() = %v, want %v", got, tt.want)
			}
		})
	}
}
