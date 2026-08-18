---
name: journalctl-traps
description: Four journalctl behaviours that no man page states and that each cost hours; the log pane is two commands because of the first
type: project
---

None of these are documented. Each was settled by building a real journal with
`systemd-journal-remote` and running the actual commands against it with
`journalctl -D` — which is what `journal_backlog_test.go` now does, so they
cannot quietly come back.

**`-g` is not a seek; `-p` is.** With `-f`, `journalctl -n 500 -g PATTERN` seeks
back 500 **raw** entries and only then applies the pattern, so it cannot see a
match older than that however many are in the journal. Measured on a
1200-entry journal whose 100 matches were the oldest: it returned **none** of
them, with the boundary exactly at the 500th entry from the end. `-p` is
unaffected — `PRIORITY` is indexed, so journalctl can seek by it. This is the
whole reason the log pane is two commands: a backlog that terminates (and can
therefore search properly) then a tail resuming from its last cursor.

**`-n 0` means "replay nothing", not "start with nothing".** It silently
defeats `--after-cursor` and `--since`. Measured: `-f --after-cursor C` replays
the entry after C; `-f -n 0 --after-cursor C` replays nothing at all. The tail
must not pass it, or everything written between the two commands is lost.

**`-n N` output order depends on which filter is set.** With `-g` it comes back
newest-first; with `-p`, oldest-first. Always pass `--reverse` explicitly and
flip it yourself, as `backlogArgs` and `fetchOlder` do, or the order silently
depends on what the user typed.

**`journalctl -g` exits 1 when nothing matches, with empty stderr.** Read as a
failure it puts a red "could not read the journal" line in the pane for the
ordinary case of a search with no hits. A *real* failure — permissions being
the usual one — exits with something to say on stderr, and that is the entire
distinction (`noMatches()`).

**How to apply:** do not collapse the two phases back into one command; do not
add `-n 0` to the tail; treat a bare exit 1 as an empty result, not an error.
Verify against a built journal rather than the manual — the manual does not
cover any of this.

Related: [[verify-remote-with-local-sshd]], [[proc-stat-guest]]
