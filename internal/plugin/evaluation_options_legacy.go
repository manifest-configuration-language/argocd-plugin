//go:build mcl_without_read_files

package plugin

import "github.com/manifest-configuration-language/mcl/pkg/mcl"

func newEvaluationOptions(options pluginOptions) mcl.EvaluationOptions {
	return mcl.EvaluationOptions{KubernetesVersion: options.kubernetesVersion}
}
