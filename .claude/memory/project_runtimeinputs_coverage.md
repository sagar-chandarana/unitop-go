---
name: runtimeinputs-coverage
description: writeShellApplication runtimeInputs must cover every command a script calls — hostname is NOT in coreutils, and gaps hide behind ambient PATH leakage
type: project
---

The flake's script apps (`nix run .#screenshots`) use `writeShellApplication`,
whose `runtimeInputs` list is the script's whole PATH contract — but nix does
not verify it, and an interactive shell's ambient PATH papers over any gap. A
missing tool therefore only surfaces in a minimal environment (container,
`nix develop -i`, CI), as a `set -euo pipefail` death partway through the run.

The trap that caught us: **`hostname` is not in coreutils.** nixpkgs coreutils
ships `hostid` but not `hostname` — that binary comes from `nettools` (or
`inetutils`). `uname -n` (coreutils) is the dependency-free spelling.

**How to apply:** when touching a script under `docs/helpers/` or its
`runtimeInputs` in `flake.nix`, grep the script for command words and check
each against the packages listed — don't assume "it ran on my machine" proves
the list. Prefer `uname -n` over `hostname`.

Found by the v0.3.1 code review (TODO.md UT-026).

Related: [[go-nix-workflow]], [[review-todo-workflow]]
