package plugin

import (
	"fmt"
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

type resourceIdentityResolver func(object map[string]any, destinationNamespace string, allowGeneratedName bool) (resourceIdentityValue, error)

func newResourceIdentityResolver(bundle mcl.Bundle) (resourceIdentityResolver, error) {
	resolver, err := mcl.NewKubernetesResourceIdentityResolver(bundle)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes resource identity resolver: %w", err)
	}

	return func(object map[string]any, destinationNamespace string, allowGeneratedName bool) (resourceIdentityValue, error) {
		apiVersion, _ := object["apiVersion"].(string)
		kind, _ := object["kind"].(string)
		metadata, _ := object["metadata"].(map[string]any)
		name, _ := metadata["name"].(string)
		generatedName := false
		if name == "" && allowGeneratedName {
			name, _ = metadata["generateName"].(string)
			generatedName = name != ""
		}
		namespace, _ := metadata["namespace"].(string)
		identity, err := resolver.Resolve(apiVersion, kind, namespace, name, destinationNamespace)
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
			generatedName: generatedName,
		}, nil
	}, nil
}

func resourceIdentity(bundle mcl.Bundle, object map[string]any, destinationNamespace string) (resourceIdentityValue, error) {
	resolver, err := newResourceIdentityResolver(bundle)
	if err != nil {
		return resourceIdentityValue{}, err
	}

	return resolver(object, destinationNamespace, false)
}
