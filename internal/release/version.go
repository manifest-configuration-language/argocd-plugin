package release

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Bump string

const (
	BumpPatch Bump = "patch"
	BumpMinor Bump = "minor"
	BumpMajor Bump = "major"
)

type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelRC     Channel = "rc"
)

// NextTag computes the next tag from the latest stable tag and all existing tags.
func NextTag(latestStable string, tags []string, bump Bump, channel Channel) (string, error) {
	version, err := parseStableTag(latestStable)
	if err != nil {
		return "", err
	}
	switch bump {
	case BumpPatch:
		if version[2] == ^uint64(0) {
			return "", errors.New("patch version overflows uint64")
		}
		version[2]++
	case BumpMinor:
		if version[1] == ^uint64(0) {
			return "", errors.New("minor version overflows uint64")
		}
		version[1]++
		version[2] = 0
	case BumpMajor:
		if version[0] == ^uint64(0) {
			return "", errors.New("major version overflows uint64")
		}
		version[0]++
		version[1] = 0
		version[2] = 0
	default:
		return "", fmt.Errorf("invalid version bump %q", bump)
	}

	stable := formatVersion(version)
	switch channel {
	case ChannelStable:
		return "v" + stable, nil
	case ChannelRC:
		prefix := "v" + stable + "-rc."
		var latestRC uint64
		for _, tag := range tags {
			sequence, found := strings.CutPrefix(tag, prefix)
			if !found {
				continue
			}
			number, err := strconv.ParseUint(sequence, 10, 64)
			if err != nil || number == 0 || strconv.FormatUint(number, 10) != sequence {
				return "", fmt.Errorf("invalid release candidate tag %q", tag)
			}
			if number > latestRC {
				latestRC = number
			}
		}
		if latestRC == ^uint64(0) {
			return "", fmt.Errorf("release candidate sequence overflows uint64 for %s", stable)
		}

		return prefix + strconv.FormatUint(latestRC+1, 10), nil
	default:
		return "", fmt.Errorf("invalid release channel %q", channel)
	}
}

// LatestStableTag returns the greatest stable vX.Y.Z tag in tags.
func LatestStableTag(tags []string) (string, error) {
	var latest string
	var latestVersion [3]uint64
	for _, tag := range tags {
		version, err := parseStableTag(tag)
		if err != nil {
			continue
		}
		if latest == "" || compareVersions(version, latestVersion) > 0 {
			latest = tag
			latestVersion = version
		}
	}
	if latest == "" {
		return "", errors.New("a stable vX.Y.Z tag is required")
	}

	return latest, nil
}

// ExistingTag returns the greatest channel-matching tag from tags when one exists.
func ExistingTag(tags []string, channel Channel) (string, bool, error) {
	var latest string
	var latestVersion [3]uint64
	var latestRC uint64
	for _, tag := range tags {
		version, sequence, matches, err := parseChannelTag(tag, channel)
		if err != nil {
			return "", false, err
		}
		if !matches {
			continue
		}
		comparison := compareVersions(version, latestVersion)
		if latest == "" || comparison > 0 || (comparison == 0 && sequence > latestRC) {
			latest = tag
			latestVersion = version
			latestRC = sequence
		}
	}

	return latest, latest != "", nil
}

func parseChannelTag(tag string, channel Channel) ([3]uint64, uint64, bool, error) {
	if channel == ChannelStable {
		version, valid := stableTagVersion(tag)

		return version, 0, valid, nil
	}
	if channel != ChannelRC {
		return [3]uint64{}, 0, false, fmt.Errorf("invalid release channel %q", channel)
	}
	base, sequenceText, found := strings.Cut(tag, "-rc.")
	if !found {
		return [3]uint64{}, 0, false, nil
	}
	version, valid := stableTagVersion(base)
	if !valid {
		return [3]uint64{}, 0, false, nil
	}
	sequence, err := strconv.ParseUint(sequenceText, 10, 64)
	if err != nil || sequence == 0 || strconv.FormatUint(sequence, 10) != sequenceText {
		return [3]uint64{}, 0, false, fmt.Errorf("invalid release candidate tag %q", tag)
	}

	return version, sequence, true, nil
}

func compareVersions(left, right [3]uint64) int {
	for index := range left {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}

	return 0
}

func parseStableTag(tag string) ([3]uint64, error) {
	version, valid := stableTagVersion(tag)
	if !valid {
		return [3]uint64{}, fmt.Errorf("a stable vX.Y.Z tag is required, got %q", tag)
	}

	return version, nil
}

func stableTagVersion(tag string) ([3]uint64, bool) {
	var version [3]uint64
	rest, ok := strings.CutPrefix(tag, "v")
	if !ok {
		return version, false
	}
	components := strings.Split(rest, ".")
	if len(components) != len(version) {
		return version, false
	}
	for index, component := range components {
		number, err := strconv.ParseUint(component, 10, 64)
		if err != nil || strconv.FormatUint(number, 10) != component {
			return version, false
		}
		version[index] = number
	}

	return version, true
}

func formatVersion(version [3]uint64) string {
	return strconv.FormatUint(version[0], 10) + "." +
		strconv.FormatUint(version[1], 10) + "." +
		strconv.FormatUint(version[2], 10)
}
