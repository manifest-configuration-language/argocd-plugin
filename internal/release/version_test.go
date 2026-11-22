package release

import (
	"strings"
	"testing"
)

const (
	latestStableTag = "v0.1.0"
	minorStableTag  = "v0.2.0"
	firstRCPatchTag = "v0.1.1-rc.1"
	firstRCMinorTag = "v0.2.0-rc.1"
)

func TestNextTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		latest      string
		existing    []string
		bump        Bump
		channel     Channel
		want        string
		wantErrText string
	}{
		{name: "patch stable", latest: latestStableTag, bump: BumpPatch, channel: ChannelStable, want: "v0.1.1"},
		{name: "minor stable", latest: latestStableTag, bump: BumpMinor, channel: ChannelStable, want: minorStableTag},
		{name: "major stable", latest: latestStableTag, bump: BumpMajor, channel: ChannelStable, want: "v1.0.0"},
		{name: "first rc", latest: latestStableTag, bump: BumpPatch, channel: ChannelRC, want: firstRCPatchTag},
		{name: "next rc", latest: latestStableTag, existing: []string{firstRCPatchTag, "v0.1.1-rc.3"}, bump: BumpPatch, channel: ChannelRC, want: "v0.1.1-rc.4"},
		{name: "stable after rc", latest: latestStableTag, existing: []string{firstRCPatchTag, "v0.1.1-rc.2"}, bump: BumpPatch, channel: ChannelStable, want: "v0.1.1"},
		{name: "rc uses requested bump", latest: latestStableTag, bump: BumpMinor, channel: ChannelRC, want: firstRCMinorTag},
		{name: "unknown bump", latest: latestStableTag, bump: "invalid", channel: ChannelStable, wantErrText: "invalid version bump"},
		{name: "unknown channel", latest: latestStableTag, bump: BumpPatch, channel: "invalid", wantErrText: "invalid release channel"},
		{name: "invalid stable tag", latest: "v0.1.0-rc.1", bump: BumpPatch, channel: ChannelStable, wantErrText: "stable vX.Y.Z"},
		{name: "version overflow", latest: "v18446744073709551615.0.0", bump: BumpMajor, channel: ChannelStable, wantErrText: "overflows"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := NextTag(test.latest, test.existing, test.bump, test.channel)
			if test.wantErrText != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErrText) {
					t.Fatalf("NextTag() error = %v, want substring %q", err, test.wantErrText)
				}

				return
			}
			if err != nil {
				t.Fatalf("NextTag() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("NextTag() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestLatestStableTag(t *testing.T) {
	t.Parallel()

	got, err := LatestStableTag([]string{firstRCMinorTag, "v0.1.9", minorStableTag, "not-a-version"})
	if err != nil {
		t.Fatal(err)
	}
	if got != minorStableTag {
		t.Fatalf("LatestStableTag() = %q, want %s", got, minorStableTag)
	}
	if _, err := LatestStableTag([]string{firstRCMinorTag, "not-a-version"}); err == nil {
		t.Fatal("LatestStableTag() error = nil, want missing stable tag error")
	}
}

func TestExistingTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tags      []string
		channel   Channel
		wantTag   string
		wantFound bool
	}{
		{name: "stable latest", tags: []string{latestStableTag, "v0.2.0-rc.2", minorStableTag}, channel: ChannelStable, wantTag: minorStableTag, wantFound: true},
		{name: "rc latest", tags: []string{firstRCPatchTag, "v0.1.1-rc.2", firstRCMinorTag}, channel: ChannelRC, wantTag: firstRCMinorTag, wantFound: true},
		{name: "channel absent", tags: []string{latestStableTag}, channel: ChannelRC},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, found, err := ExistingTag(test.tags, test.channel)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.wantTag || found != test.wantFound {
				t.Fatalf("ExistingTag() = (%q, %t), want (%q, %t)", got, found, test.wantTag, test.wantFound)
			}
		})
	}
}
