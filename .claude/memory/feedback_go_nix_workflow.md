---
name: go-nix-workflow
description: The build loop for unitop — go in a nix shell, vendor + git add before nix build, and nix build runs the tests in a sandbox
type: feedback
---

There is no Go toolchain on PATH on this machine; get one with
`nix shell nixpkgs#go` (add `nixpkgs#gotools` for `goimports`), or `nix develop`
for the project dev shell. Point `GOPATH` at a scratch directory so the module
cache does not land in the repo.

Fast loop: `cd src && go test ./...` — under a second, no host or network
needed. Only reach for `nix build` when you actually want the packaged binary.

Two things that fail confusingly:

- **New files must be `git add`ed before `nix build`/`nix run`.** Flakes only
  see git-tracked files, so an untracked `foo.go` surfaces as a compile error
  about a missing symbol rather than a missing file.
- **`go mod vendor` must be re-run and the vendor tree committed** after any
  dependency change. `vendorHash = null` means nix trusts whatever is checked
  in, so a stale vendor directory is a silent mismatch.

**`nix build` runs the test suite** (`buildGoModule` sets `doCheck = true`) in a
sandbox with **no network, no dbus and no running systemd**. So:

- No test may shell out to `systemctl` or `journalctl`, or depend on a host.
- `remote_test.go` covers the remote path by running the generated shell script
  through plain `sh` and reading `/proc` — both of which the sandbox does have.
- If you want a one-off probe against real systemd, write it in the scratchpad,
  run it with `go test -run`, and delete it. Do not commit it.

The package is built **static** (`env.CGO_ENABLED = 0`, `ldflags -s -w`) on
purpose, so the binary can be scp'd to any host regardless of libc. Verify with
`file result/bin/unitop` after touching the flake.

Related: [[verify-tui-with-pty]]
