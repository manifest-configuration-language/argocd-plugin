//go:build !mcl_without_read_files

package plugin

import (
	"os"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

func newEvaluationOptions(options pluginOptions) mcl.EvaluationOptions {
	return mcl.EvaluationOptions{
		KubernetesVersion: options.kubernetesVersion,
		CacheDirectory:    os.Getenv("MCL_CACHE_DIRECTORY"),
	}
}
