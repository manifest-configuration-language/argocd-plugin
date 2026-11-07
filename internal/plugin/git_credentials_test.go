//go:build !mcl_without_git_credentials

package plugin

import (
	"context"
	"strings"
	"testing"
)

const repositoryURL = "https://github.com/example/project"

func TestSameGitRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		applicationRepo string
		dependencyRepo  string
		want            bool
	}{
		{name: "same repository with optional git suffix", applicationRepo: repositoryURL + ".git", dependencyRepo: "git+" + repositoryURL, want: true},
		{name: "different repository", applicationRepo: repositoryURL, dependencyRepo: "https://github.com/example/other"},
		{name: "different host", applicationRepo: repositoryURL, dependencyRepo: "https://git.example.com/example/project"},
		{name: "non HTTPS application source", applicationRepo: "ssh://git@github.com/example/project", dependencyRepo: "https://github.com/example/project"},
		{name: "credentials in dependency URL", applicationRepo: "https://github.com/example/project", dependencyRepo: "https://token@github.com/example/project"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := sameGitRepository(test.applicationRepo, test.dependencyRepo); got != test.want {
				t.Fatalf("sameGitRepository(%q, %q) = %t, want %t", test.applicationRepo, test.dependencyRepo, got, test.want)
			}
		})
	}
}

func TestGitCredentialProviderIsDisabledWithoutArgoAskPass(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "")
	t.Setenv("ARGOCD_BINARY_NAME", "")
	t.Setenv("ARGOCD_GIT_ASKPASS_NONCE", "")

	if provider := gitCredentialProvider(); provider != nil {
		t.Fatal("gitCredentialProvider() returned a provider without Argo CD ASKPASS environment")
	}
}

func TestGitCredentialProviderUsesArgoSourceRepositoryURL(t *testing.T) {
	t.Setenv("GIT_ASKPASS", "argocd")
	t.Setenv("ARGOCD_BINARY_NAME", "argocd-git-ask-pass")
	t.Setenv("ARGOCD_GIT_ASKPASS_NONCE", "nonce")
	t.Setenv("ARGOCD_APP_SOURCE_REPO_URL", repositoryURL+".git")

	var prompts []string
	provider := gitCredentialProviderWithAsk(func(_ context.Context, prompt string) (string, error) {
		prompts = append(prompts, prompt)
		if strings.HasPrefix(prompt, "Username") {
			return "git-user", nil
		}

		return "git-password", nil
	})
	if provider == nil {
		t.Fatal("gitCredentialProviderWithAsk() returned nil with Argo CD ASKPASS environment")
	}
	credentials, err := provider(t.Context(), "git+"+repositoryURL)
	if err != nil {
		t.Fatalf("credential provider error = %v", err)
	}
	if credentials.Username != "git-user" || credentials.Password != "git-password" {
		t.Fatalf("credential provider returned %+v, want credentials for the matching Application repository", credentials)
	}
	if len(prompts) != 2 || !strings.HasPrefix(prompts[0], "Username") || !strings.HasPrefix(prompts[1], "Password") {
		t.Fatalf("credential prompts = %q, want username and password prompts", prompts)
	}
	if _, err := provider(t.Context(), "git+https://github.com/example/other"); err != nil {
		t.Fatalf("credential provider rejected a different repository: %v", err)
	}
	if len(prompts) != 2 {
		t.Fatalf("credential helper was called for a different repository: %q", prompts)
	}
}
