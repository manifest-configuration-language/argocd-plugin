//go:build !mcl_without_git_credentials

package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

const argocdBinaryPath = "/var/run/argocd/argocd"

func configureGitCredentialProvider(options *mcl.EvaluationOptions) {
	options.GitCredentialProvider = gitCredentialProvider()
}

func gitCredentialProvider() mcl.GitCredentialProvider {
	return gitCredentialProviderWithAsk(askGitCredential)
}

func gitCredentialProviderWithAsk(ask func(context.Context, string) (string, error)) mcl.GitCredentialProvider {
	if os.Getenv("GIT_ASKPASS") != "argocd" || os.Getenv("ARGOCD_BINARY_NAME") != "argocd-git-ask-pass" || os.Getenv("ARGOCD_GIT_ASKPASS_NONCE") == "" {
		return nil
	}

	return func(ctx context.Context, source string) (mcl.GitCredentials, error) {
		origin := os.Getenv("ARGOCD_APP_SOURCE_REPO_URL")
		if !sameGitRepository(origin, source) {
			return mcl.GitCredentials{}, nil
		}
		username, err := ask(ctx, "Username for '"+source+"':")
		if err != nil {
			return mcl.GitCredentials{}, err
		}
		password, err := ask(ctx, "Password for '"+source+"':")
		if err != nil {
			return mcl.GitCredentials{}, err
		}

		return mcl.GitCredentials{Username: username, Password: password}, nil
	}
}

func askGitCredential(ctx context.Context, prompt string) (string, error) {
	command := exec.CommandContext(ctx, argocdBinaryPath, prompt)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return "", errors.New("the Argo CD Git credential helper failed")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(stdout.String(), "\n"), "\r")
	if value == "" {
		return "", fmt.Errorf("the Argo CD Git credential helper returned an empty %s", strings.SplitN(prompt, " ", 2)[0])
	}

	return value, nil
}

func sameGitRepository(applicationRepo, dependencyRepo string) bool {
	application, err := url.Parse(applicationRepo)
	if err != nil || application.Scheme != "https" || application.Host == "" || application.User != nil || application.RawQuery != "" || application.Fragment != "" {
		return false
	}
	dependency, err := url.Parse(strings.TrimPrefix(dependencyRepo, "git+"))
	if err != nil || dependency.Scheme != "https" || dependency.Host == "" || dependency.User != nil || dependency.RawQuery != "" || dependency.Fragment != "" {
		return false
	}
	application.Path = strings.TrimSuffix(application.Path, ".git")
	dependency.Path = strings.TrimSuffix(dependency.Path, ".git")

	return strings.EqualFold(application.Host, dependency.Host) && strings.TrimSuffix(application.Path, "/") == strings.TrimSuffix(dependency.Path, "/")
}
