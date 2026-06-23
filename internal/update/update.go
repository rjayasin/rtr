// Package update self-updates the rtr binary from the prebuilt archives
// published on GitHub Releases. It is used by the `rtr update` command and by a
// non-blocking "update available" check at startup.
package update

import (
	"context"
	"fmt"
	"os"
	"strings"

	su "github.com/creativeprojects/go-selfupdate"

	"github.com/rjayasin/rtr/internal/util"
)

// Slug is the GitHub owner/repo that release archives are fetched from.
const Slug = "rjayasin/rtr"

func newUpdater() (*su.Updater, error) {
	// Validate downloads against the checksums.txt asset GoReleaser publishes.
	return su.NewUpdater(su.Config{
		Validator: &su.ChecksumValidator{UniqueFilename: "checksums.txt"},
	})
}

// Latest returns the newest published version and whether it is strictly newer
// than current. Non-release builds (dev/commit-hash versions) never report an
// update so local builds aren't nagged.
func Latest(ctx context.Context, current string) (latest string, newer bool, err error) {
	up, err := newUpdater()
	if err != nil {
		return "", false, err
	}
	rel, found, err := up.DetectLatest(ctx, su.ParseSlug(Slug))
	if err != nil {
		return "", false, err
	}
	if !found || rel == nil {
		return "", false, nil
	}
	return rel.Version(), isReleaseVersion(current) && rel.GreaterThan(current), nil
}

// Apply replaces the running binary with the latest release when it is newer.
// A non-release build is always updated onto the latest published version.
// It returns the resulting version and whether the binary was replaced. status,
// if non-nil, receives human-readable progress messages as each step runs.
func Apply(ctx context.Context, current string, status func(string)) (version string, updated bool, err error) {
	report := func(format string, a ...any) {
		if status != nil {
			status(fmt.Sprintf(format, a...))
		}
	}

	up, err := newUpdater()
	if err != nil {
		return "", false, err
	}
	report("contacting github.com/%s…", Slug)
	rel, found, err := up.DetectLatest(ctx, su.ParseSlug(Slug))
	if err != nil {
		return "", false, err
	}
	if !found || rel == nil {
		return "", false, fmt.Errorf("no published release found for %s", Slug)
	}
	if isReleaseVersion(current) && !rel.GreaterThan(current) {
		return rel.Version(), false, nil
	}

	exe, err := os.Executable()
	if err != nil {
		return "", false, err
	}
	report("latest is %s; downloading %s (%s)…", rel.Version(), rel.AssetName, util.HumanBytes(int64(rel.AssetByteSize)))
	report("verifying checksum and replacing %s…", exe)
	if err := up.UpdateTo(ctx, rel, exe); err != nil {
		return "", false, err
	}
	return rel.Version(), true, nil
}

// isReleaseVersion reports whether s looks like a real release version (vX.Y...),
// as opposed to a "dev" or commit-hash build that can't be ordered against tags.
func isReleaseVersion(s string) bool {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return false
	}
	for _, p := range parts[:2] { // major and minor must be plain integers
		if p == "" {
			return false
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return false
			}
		}
	}
	return true
}
