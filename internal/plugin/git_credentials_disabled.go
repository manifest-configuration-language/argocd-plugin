//go:build mcl_without_git_credentials

package plugin

import "github.com/manifest-configuration-language/mcl/pkg/mcl"

func configureGitCredentialProvider(*mcl.EvaluationOptions) {}
