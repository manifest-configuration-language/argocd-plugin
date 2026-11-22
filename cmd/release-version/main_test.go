package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifest-configuration-language/argocd-plugin/internal/release"
)

func TestDetermineTarget(t *testing.T) {
	t.Parallel()

	t.Run("computes next release candidate", func(t *testing.T) {
		t.Parallel()

		root, stableSHA := newReleaseRepository(t)
		currentSHA := commitReleaseFile(t, root, "candidate")
		if got, err := determineTarget(t.Context(), root, currentSHA, release.BumpPatch, release.ChannelRC); err != nil {
			t.Fatal(err)
		} else if got != (target{version: "0.1.1-rc.1", tag: "v0.1.1-rc.1"}) {
			t.Fatalf("determineTarget() = %+v, want v0.1.1-rc.1", got)
		}
		if currentSHA == stableSHA {
			t.Fatal("test repository did not create a new commit")
		}
	})

	t.Run("reuses channel tag on current commit", func(t *testing.T) {
		t.Parallel()

		root, _ := newReleaseRepository(t)
		currentSHA := commitReleaseFile(t, root, "candidate")
		git(t, root, "tag", "v0.1.1-rc.2", currentSHA)
		got, err := determineTarget(t.Context(), root, currentSHA, release.BumpPatch, release.ChannelRC)
		if err != nil {
			t.Fatal(err)
		}
		if want := (target{version: "0.1.1-rc.2", tag: "v0.1.1-rc.2"}); got != want {
			t.Fatalf("determineTarget() = %+v, want %+v", got, want)
		}
	})

	t.Run("rejects commit older than latest stable", func(t *testing.T) {
		t.Parallel()

		root, oldSHA := newReleaseRepository(t)
		newSHA := commitReleaseFile(t, root, "new stable")
		git(t, root, "tag", "v0.1.1", newSHA)
		if _, err := determineTarget(t.Context(), root, oldSHA, release.BumpPatch, release.ChannelRC); err == nil {
			t.Fatal("determineTarget() error = nil, want stale commit error")
		}
	})

	t.Run("rejects commit diverged from latest stable", func(t *testing.T) {
		t.Parallel()

		root, _ := newReleaseRepository(t)
		git(t, root, "checkout", "--orphan", "unrelated")
		currentSHA := commitReleaseFile(t, root, "unrelated")
		if _, err := determineTarget(t.Context(), root, currentSHA, release.BumpPatch, release.ChannelStable); err == nil {
			t.Fatal("determineTarget() error = nil, want divergent history error")
		}
	})
}

func TestDetermineTargetRejectsInvalidCommitSHA(t *testing.T) {
	t.Parallel()

	if _, err := determineTarget(t.Context(), t.TempDir(), "--help", release.BumpPatch, release.ChannelStable); err == nil {
		t.Fatal("determineTarget() error = nil, want invalid commit SHA error")
	}
}

func newReleaseRepository(t *testing.T) (string, string) {
	t.Helper()

	root := t.TempDir()
	git(t, root, "init", "--initial-branch=main")
	git(t, root, "config", "user.name", "Release test")
	git(t, root, "config", "user.email", "release-test@example.invalid")
	sha := commitReleaseFile(t, root, "stable")
	git(t, root, "tag", "v0.1.0", sha)

	return root, sha
}

func commitReleaseFile(t *testing.T, root, contents string) string {
	t.Helper()

	if err := os.WriteFile(filepath.Join(root, "release.txt"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "release.txt")
	git(t, root, "commit", "-m", contents)

	return git(t, root, "rev-parse", "HEAD")
}

func git(t *testing.T, root string, args ...string) string {
	t.Helper()

	command := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...) // #nosec G204 -- tests invoke fixed Git subcommands against a temporary repository.
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}

	return strings.TrimSpace(string(output))
}
