package plugin

import (
	"testing"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

func TestResourceIdentityNormalizesNamespaceAndAPIVersion(t *testing.T) {
	t.Parallel()

	for _, destinationNamespace := range []string{"", "workloads"} {
		t.Run(destinationNamespace, func(t *testing.T) {
			t.Parallel()

			implicitNamespace := map[string]any{
				"apiVersion": "apps/v1",
				"kind":       "Deployment",
				"metadata":   map[string]any{"name": "worker"},
			}
			explicitNamespace := map[string]any{
				"apiVersion": "apps/v1beta1",
				"kind":       "Deployment",
				"metadata":   map[string]any{"name": "worker", "namespace": "default"},
			}
			if destinationNamespace != "" {
				explicitNamespace["metadata"].(map[string]any)["namespace"] = destinationNamespace
			}
			implicit, err := resourceIdentity(mcl.Bundle{}, implicitNamespace, destinationNamespace)
			if err != nil {
				t.Fatalf("resourceIdentity() for omitted namespace: %v", err)
			}
			explicit, err := resourceIdentity(mcl.Bundle{}, explicitNamespace, destinationNamespace)
			if err != nil {
				t.Fatalf("resourceIdentity() for explicit namespace: %v", err)
			}
			if implicit.key != explicit.key {
				t.Fatalf("resource identities differ: implicit=%q explicit=%q", implicit.key, explicit.key)
			}
		})
	}
}
