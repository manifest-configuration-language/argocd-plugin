//go:build !mcl_without_read_files

package plugin

import (
	"fmt"
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

func resourceIdentity(bundle mcl.Bundle, object map[string]any, destinationNamespace string) (resourceIdentityValue, error) {
	apiVersion, _ := object["apiVersion"].(string)
	kind, _ := object["kind"].(string)
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	identity, err := mcl.ResolveKubernetesResourceIdentity(bundle, apiVersion, kind, namespace, name, destinationNamespace)
	if err != nil {
		return resourceIdentityValue{}, fmt.Errorf("resolve Kubernetes resource identity: %w", err)
	}
	groupKindName := identity.APIGroup + "/" + identity.Kind
	display := groupKindName + "/" + identity.Namespace + "/" + identity.Name

	scope := resourceScopeUnknown
	switch identity.Scope {
	case mcl.KubernetesResourceScopeNamespaced:
		scope = resourceScopeNamespaced
	case mcl.KubernetesResourceScopeCluster:
		scope = resourceScopeCluster
	case mcl.KubernetesResourceScopeUnknown:
	}

	return resourceIdentityValue{
		key:           strings.Join([]string{identity.APIGroup, identity.Kind, identity.Namespace, identity.Name}, "\x00"),
		groupKindName: groupKindName,
		display:       display,
		name:          identity.Name,
		namespace:     identity.Namespace,
		scope:         scope,
	}, nil
}
