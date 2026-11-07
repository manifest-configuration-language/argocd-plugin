//go:build !mcl_without_git_credentials

package plugin

import "testing"

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
