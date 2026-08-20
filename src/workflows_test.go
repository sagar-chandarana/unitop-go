package main

import (
	"os"
	"os/exec"
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

// The release-tag validation is security-load-bearing, so exercise the ACTUAL
// shell from release.yml (extracted, not re-implemented, so it cannot drift)
// in a throwaway git repo across every ref shape: an annotated and a
// lightweight v-tag on HEAD accept; a non-v ref, a v-PREFIXED branch (v*
// namespace but not a tag), a bare SHA, a missing tag, and a real tag whose
// commit is not HEAD all reject. Skips where ../.github
// or git/sh are unavailable (the nix build sandbox, minimal CI).
func TestReleaseTagValidationShell(t *testing.T) {
	dir := filepath.Join("..", ".github", "workflows")
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Skip("no ../.github in this sandbox")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	script := extractRunBlock(t, filepath.Join(dir, "release.yml"), "Validate the release tag")

	repo := t.TempDir()
	git := func(args ...string) string {
		c := exec.Command("git", args...)
		c.Dir = repo
		c.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "one")
	c1 := git("rev-parse", "HEAD")
	git("tag", "-a", "v1.0.0", "-m", "annotated") // annotated on HEAD
	git("tag", "v1.0.1")                          // lightweight on HEAD
	git("tag", "-a", "v0.9.0", "-m", "old", c1)   // will become not-HEAD after a 2nd commit
	if err := os.WriteFile(filepath.Join(repo, "f"), []byte("2"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("commit", "-aqm", "two") // HEAD moves off v0.9.0
	// A v-PREFIXED branch: it clears the v* namespace guard but is not a
	// tag, so the refs/tags lookup must still reject it.
	git("checkout", "-q", "-b", "v-feature")

	run := func(tag string) error {
		c := exec.Command("sh", "-c", script)
		c.Dir = repo
		c.Env = append(os.Environ(), "TAG="+tag)
		return c.Run()
	}
	// Accept: annotated and lightweight v-tags, checked out at their commit.
	git("checkout", "-q", "v1.0.0")
	if err := run("v1.0.0"); err != nil {
		t.Errorf("annotated tag on HEAD rejected: %v", err)
	}
	git("checkout", "-q", "v1.0.1")
	if err := run("v1.0.1"); err != nil {
		t.Errorf("lightweight tag on HEAD rejected: %v", err)
	}
	// Reject: everything else.
	git("checkout", "-q", "main")
	reject := map[string]string{
		"non-v ref (main)":  "main",
		"v-prefixed branch": "v-feature", // v* namespace but not a tag
		"bare SHA":          c1,
		"missing tag":       "v9.9.9",
		"tag not at HEAD":   "v0.9.0", // HEAD is "two", tag is on "one"
	}
	for name, tag := range reject {
		if err := run(tag); err == nil {
			t.Errorf("%s (%q) was accepted", name, tag)
		}
	}
}

// extractRunBlock pulls the shell of a named step's `run: |` block out of a
// workflow, dedented, so a test runs the real thing rather than a copy.
func extractRunBlock(t *testing.T, path, stepName string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	i := 0
	for i < len(lines) && !strings.Contains(lines[i], "name: "+stepName) {
		i++
	}
	for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "run: |") {
		i++
	}
	if i >= len(lines) {
		t.Fatalf("no run: block for step %q", stepName)
	}
	indent := strings.IndexByte(lines[i], 'r') // column of `run:`
	var body []string
	for i++; i < len(lines); i++ {
		l := lines[i]
		if strings.TrimSpace(l) == "" {
			body = append(body, "")
			continue
		}
		lead := len(l) - len(strings.TrimLeft(l, " "))
		if lead <= indent {
			break // dedented out of the block
		}
		body = append(body, l[min(indent+2, lead):])
	}
	return strings.Join(body, "\n")
}
