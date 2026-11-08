//go:build mcl_without_read_files

package plugin

import (
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

type resourceIdentityResolver func(object map[string]any, destinationNamespace string, allowGeneratedName bool) (resourceIdentityValue, error)

func newResourceIdentityResolver(bundle mcl.Bundle) (resourceIdentityResolver, error) {
	return func(object map[string]any, _ string, allowGeneratedName bool) (resourceIdentityValue, error) {
		return legacyResourceIdentity(object, allowGeneratedName)
	}, nil
}

func resourceIdentity(_ mcl.Bundle, object map[string]any, _ string) (resourceIdentityValue, error) {
	return legacyResourceIdentity(object, false)
}

func legacyResourceIdentity(object map[string]any, allowGeneratedName bool) (resourceIdentityValue, error) {
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
	group, _, hasVersion := strings.Cut(apiVersion, "/")
	if !hasVersion {
		group = ""
	}
	groupKindName := group + "/" + kind
	display := groupKindName + "/" + namespace + "/" + name

	return resourceIdentityValue{
		key:           strings.Join([]string{group, kind, namespace, name}, "\x00"),
		groupKindName: groupKindName,
		display:       display,
		name:          name,
		namespace:     namespace,
		scope:         resourceScopeUnknown,
		generatedName: generatedName,
	}, nil
}
