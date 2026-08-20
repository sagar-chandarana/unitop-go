package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Static supply-chain / release-integrity invariants over the CI workflows.
// actionlint and zizmor (run separately; both clean at the reviewed revision)
// are the linters; this pins the invariants so a regression is caught in the
// ordinary test run too. It reads ../.github, which the src-only nix build
// sandbox does not contain, so it skips there and runs in CI and locally.
func TestWorkflowsAreHardened(t *testing.T) {
	dir := filepath.Join("..", ".github", "workflows")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("no ../.github in this sandbox (nix build); runs in CI and locally")
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	ci, release := read("ci.yml"), read("release.yml")

	// 1. Every `uses:` is pinned to a full 40-hex commit SHA — no @v7, no
	// branch, no short SHA.
	usesLine := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*(\S+)`)
	pinned := regexp.MustCompile(`^[^@\s]+@[0-9a-f]{40}\b`)
	total := 0
	for _, wf := range []string{ci, release} {
		for _, m := range usesLine.FindAllStringSubmatch(wf, -1) {
			total++
			if !pinned.MatchString(m[1]) {
				t.Errorf("action is not pinned to a full SHA: %q", m[1])
			}
		}
	}
	if total < 5 {
		t.Errorf("found only %d `uses:` — the regex missed some", total)
	}

	// 2. Least privilege: the repo default is read; only the release job is
	// granted write.
	if !strings.Contains(release, "permissions:\n  contents: read") {
		t.Error("release.yml repo-default permissions are not contents: read")
	}
	if !strings.Contains(release, "    permissions:\n      contents: write") {
		t.Error("release.yml does not scope contents: write to the release job")
	}

	// 3. The dispatch tag is validated: v* only, the tag must exist, and the
	// checkout must be exactly its commit.
	for _, want := range []string{
		"Validate the release tag",
		`refs/tags/$TAG^{commit}`,
		"refusing a non-v* release ref",
		"git rev-parse --verify HEAD",
	} {
		if !strings.Contains(release, want) {
			t.Errorf("release.yml is missing the tag guard: %q", want)
		}
	}

	// 4. The publish verifies the tag.
	if !strings.Contains(release, "--verify-tag") {
		t.Error("release.yml does not publish with --verify-tag")
	}

	// 5. Every checkout drops the persisted credential (artipacked), so the
	// token cannot leak through the .git dir.
	checkouts := strings.Count(ci, "uses: actions/checkout@") + strings.Count(release, "uses: actions/checkout@")
	guards := strings.Count(ci, "persist-credentials: false") + strings.Count(release, "persist-credentials: false")
	if guards != checkouts {
		t.Errorf("%d checkouts but %d persist-credentials: false guards", checkouts, guards)
	}

	// 6. The release build does not trust a shared cache (cache-poisoning on a
	// contents: write job).
	if !strings.Contains(release, "cache: false") {
		t.Error("release.yml setup-go still trusts the module cache")
	}
}
