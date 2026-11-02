/*
Copyright 2026 Red Hat, Inc.

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

package tls

import (
	"context"
	"testing"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func newFakeClientWithAPIServer(profile *configv1.TLSSecurityProfile) client.Client {
	scheme := runtime.NewScheme()
	_ = configv1.AddToScheme(scheme)
	apiServer := &configv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster"},
		Spec:       configv1.APIServerSpec{TLSSecurityProfile: profile},
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(apiServer).Build()
}

func TestGetTLSProfileValuesCurvePreferences(t *testing.T) {
	customProfile := func(groups []configv1.TLSGroup) *configv1.TLSSecurityProfile {
		return &configv1.TLSSecurityProfile{
			Type: configv1.TLSProfileCustomType,
			Custom: &configv1.CustomTLSProfile{
				TLSProfileSpec: configv1.TLSProfileSpec{
					Ciphers:       []string{"ECDHE-RSA-AES128-GCM-SHA256"},
					MinTLSVersion: configv1.VersionTLS12,
					Groups:        groups,
				},
			},
		}
	}

	testCases := []struct {
		name             string
		tlsGroupsEnabled bool
		profile          *configv1.TLSSecurityProfile
		expected         string
	}{
		{
			name:             "feature disabled returns empty even with groups",
			tlsGroupsEnabled: false,
			profile:          customProfile([]configv1.TLSGroup{configv1.TLSGroupX25519, configv1.TLSGroupSecP256r1}),
			expected:         "",
		},
		{
			name:             "feature enabled maps groups to numeric curve IDs in order",
			tlsGroupsEnabled: true,
			profile: customProfile([]configv1.TLSGroup{
				configv1.TLSGroupX25519,    // tls.X25519 = 29
				configv1.TLSGroupSecP256r1, // tls.CurveP256 = 23
				configv1.TLSGroupSecP384r1, // tls.CurveP384 = 24
			}),
			expected: "29,23,24",
		},
		{
			name:             "feature enabled but no groups returns empty",
			tlsGroupsEnabled: true,
			profile:          customProfile(nil),
			expected:         "",
		},
		{
			name:             "unrecognized groups are skipped",
			tlsGroupsEnabled: true,
			profile:          customProfile([]configv1.TLSGroup{configv1.TLSGroupX25519, "bogus-group"}),
			expected:         "29",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			c := newFakeClientWithAPIServer(tc.profile)
			_, _, got, _, err := GetTLSProfileValues(context.Background(), c, tc.tlsGroupsEnabled, nil)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, got)
		})
	}
}
