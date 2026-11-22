package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/manifest-configuration-language/argocd-plugin/internal/release"
)

type target struct {
	version string
	tag     string
}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get repository directory: %w", err)
	}
	sha := os.Getenv("GITHUB_SHA")
	if err := validateCommitSHA(sha); err != nil {
		return err
	}
	result, err := determineTarget(ctx, root, sha, release.Bump(os.Getenv("BUMP")), release.Channel(os.Getenv("CHANNEL")))
	if err != nil {
		return err
	}
	outputPath := os.Getenv("GITHUB_OUTPUT")
	if outputPath == "" {
		return errors.New("GITHUB_OUTPUT is required")
	}
	output, err := os.OpenFile(outputPath, os.O_APPEND|os.O_WRONLY, 0) // #nosec G703 -- GITHUB_OUTPUT is a runner-managed output file path.
	if err != nil {
		return fmt.Errorf("open GitHub output file: %w", err)
	}
	defer output.Close()
	if _, err := fmt.Fprintf(output, "version=%s\nrelease_tag=%s\n", result.version, result.tag); err != nil {
		return fmt.Errorf("write release target: %w", err)
	}

	return nil
}

func validateCommitSHA(sha string) error {
	if (len(sha) != 40 && len(sha) != 64) || !isHex(sha) {
		return errors.New("GITHUB_SHA must be a 40- or 64-character hexadecimal object ID")
	}

	return nil
}

func isHex(value string) bool {
	_, err := hex.DecodeString(value)

	return err == nil
}

func determineTarget(ctx context.Context, root, sha string, bump release.Bump, channel release.Channel) (target, error) {
	if err := validateCommitSHA(sha); err != nil {
		return target{}, err
	}

	tagsOutput, err := gitOutput(ctx, root, "tag", "--list", "v*")
	if err != nil {
		return target{}, err
	}
	tags := lines(tagsOutput)
	pointedOutput, err := gitOutput(ctx, root, "tag", "--points-at", sha)
	if err != nil {
		return target{}, err
	}
	if tag, found, err := release.ExistingTag(lines(pointedOutput), channel); err != nil {
		return target{}, err
	} else if found {
		return target{version: strings.TrimPrefix(tag, "v"), tag: tag}, nil
	}

	latestStable, err := release.LatestStableTag(tags)
	if err != nil {
		return target{}, err
	}
	latestSHA, err := gitOutput(ctx, root, "rev-list", "-n", "1", latestStable)
	if err != nil {
		return target{}, err
	}
	latestSHA = strings.TrimSpace(latestSHA)
	if latestSHA != sha {
		command := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", latestSHA, sha) // #nosec G702 -- both values are Git object IDs read from the repository or validated above.
		if err := command.Run(); err != nil {
			if exitErr, ok := errors.AsType[*exec.ExitError](err); ok && exitErr.ExitCode() == 1 {
				return target{}, fmt.Errorf("refusing to release commit %s because it does not descend from latest stable tag %s", sha, latestStable)
			}

			return target{}, fmt.Errorf("check release commit ancestry: %w", err)
		}
	}

	tag, err := release.NextTag(latestStable, tags, bump, channel)
	if err != nil {
		return target{}, err
	}

	return target{version: strings.TrimPrefix(tag, "v"), tag: tag}, nil
}

func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...) // #nosec G204,G702 -- callers provide fixed Git subcommands and validated object IDs.
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("run git %s: %w", strings.Join(args, " "), err)
	}

	return string(output), nil
}

func lines(value string) []string {
	var result []string
	scanner := bufio.NewScanner(strings.NewReader(value))
	for scanner.Scan() {
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			result = append(result, line)
		}
	}

	return result
}
