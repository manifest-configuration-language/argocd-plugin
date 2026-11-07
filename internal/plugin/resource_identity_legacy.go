//go:build mcl_without_read_files

package plugin

import (
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

func resourceIdentity(_ mcl.Bundle, object map[string]any, _ string) (resourceIdentityValue, error) {
	apiVersion, _ := object["apiVersion"].(string)
	kind, _ := object["kind"].(string)
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
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
	}, nil
}
