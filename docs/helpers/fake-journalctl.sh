#!/bin/sh
# A journalctl that serves a synthetic journal, so the screenshots can be taken
# anywhere — including a machine whose own journal is unreadable, which is what
# a sandbox or an unprivileged account gives you.
#
# It builds one journal per unit, on demand and cached, choosing messages that
# suit that unit, so the log pane reads as that unit's own log rather than as
# filler. screenshot.sh wires it up for you:
#
#   FAKE_JOURNAL=1 docs/helpers/screenshot.sh docs/main.png 132 30 …
#
# It has to be reached as `journalctl` on PATH, which is why screenshot.sh
# symlinks it into a directory of its own rather than adding docs/helpers.
#
# The alternative is screenshotting a real host, which means publishing somebody's
# real log lines or hand-editing them afterwards. The unit table and the host
# stats are still real; only the journal is invented.
set -eu
umask 077 # narrows exposure; ownership of a pre-created dir still wins
# Per-user so accounts do not trip over each other's caches. A predictable
# path is still pre-creatable by another user — that hardening is deliberately
# deferred (TODO UT-025's cross-UID variant).
CACHE=${SHOTRIG_CACHE:-${TMPDIR:-/tmp}/unitop-shot-journals-$(id -u)}
# Overridable so the nix app can supply them; the defaults suit a NixOS host.
# REAL must stay an absolute path: this script is reached *as* journalctl on
# PATH, so looking it up there would find itself.
REMOTE=${JOURNAL_REMOTE:-/run/current-system/sw/lib/systemd/systemd-journal-remote}
REAL=${REAL_JOURNALCTL:-/run/current-system/sw/bin/journalctl}

# Split off -u <unit> and keep everything else as real positional parameters:
# hand-quoting into a string for a later eval broke on any argument holding a
# quote — unitop passes the user's / search pattern verbatim as -g — and let
# $(…) inside one execute.
unit=""
found=0
skip=0
n=$#
i=0
while [ "$i" -lt "$n" ]; do
	a=$1
	shift
	i=$((i + 1))
	if [ "$skip" = 1 ]; then unit=$a; skip=0; continue; fi
	if [ "$found" = 0 ] && [ "$a" = "-u" ]; then found=1 skip=1; continue; fi
	set -- "$@" "$a"
done
if [ "$skip" = 1 ]; then
	echo "fake-journalctl: -u needs a unit" >&2
	exit 1
fi
[ -n "$unit" ] || unit=systemd-logind.service
short=$(printf '%s' "$unit" | sed 's/\.service$//')
case "$short" in
*logind*) MSGS="6|New session 4081 of user root.
6|New session c3 of user gdm.
6|Watching system buttons on /dev/input/event2 (Power Button)
6|Session 4079 logged out. Waiting for processes to exit.
6|Removed session 4079.
6|New session 4082 of user deploy.
5|Session 4082 logged out. Waiting for processes to exit.
6|Removed session 4082.
6|New session 4083 of user root.
6|Power key pressed short.
4|Failed to abandon session scope: Transport endpoint is not connected
6|Removed session 4083.
6|New session 4084 of user deploy.
6|Session 4080 logged out. Waiting for processes to exit.
6|Removed session 4080." ;;
*sshd*) MSGS="6|Accepted publickey for root from 10.0.4.17 port 52344 ssh2: ED25519 SHA256:qL7…
6|pam_unix(sshd:session): session opened for user root(uid=0)
6|Received disconnect from 10.0.4.17 port 52344:11: disconnected by user
6|pam_unix(sshd:session): session closed for user root
4|Invalid user admin from 45.134.26.9 port 41022
3|Connection closed by invalid user admin 45.134.26.9 port 41022 [preauth]
6|Accepted publickey for deploy from 10.0.4.201 port 41880 ssh2: ED25519 SHA256:8xR…
6|pam_unix(sshd:session): session opened for user deploy(uid=1001)
6|Received disconnect from 10.0.4.201 port 41880:11: disconnected by user
6|pam_unix(sshd:session): session closed for user deploy" ;;
*networkd* | *NetworkManager* | *resolved*) MSGS="6|eth0: Link UP
6|eth0: Gained carrier
6|eth0: DHCPv4 address 10.0.4.23/24 via 10.0.4.1
6|Using system hostname 'server1.local'
5|eth0: DHCPv4 lease lost
6|eth0: DHCPv4 address 10.0.4.23/24 via 10.0.4.1
6|Flushed all DNS caches
6|eth0: Gained IPv6LL
6|Sync-ing state to /run
6|eth0: Configured" ;;
*journald*) MSGS="6|System Journal (/var/log/journal/5f2a…) is 1.2G, max 4.0G, 2.7G free.
6|Time spent on flushing to /var/log/journal/5f2a… is 41.882ms for 1041 entries.
4|Suppressed 214 messages from user@1001.service
6|Journal started
6|Runtime Journal (/run/log/journal/5f2a…) is 8.0M, max 393.1M, 385.1M free.
6|Rotating system journal.
6|Vacuuming done, freed 0B of archived journals from /var/log/journal.
6|Forwarding to syslog missed 0 messages." ;;
*user@*) MSGS="6|Queued start job for default target Main User Target.
6|Created slice User Application Slice.
6|Reached target Paths.
6|Listening on GnuPG cryptographic agent and passphrase cache.
6|Starting D-Bus User Message Bus...
6|Started D-Bus User Message Bus.
6|Reached target Sockets.
6|Reached target Basic System.
6|Startup finished in 412ms.
6|Reached target Main User Target.
5|pipewire.service: Consumed 1.204s CPU time.
6|Started Sound Service.
6|gpg-agent-ssh.socket: Deactivated successfully.
6|Stopped target Sockets.
6|Reached target Main User Target." ;;
*) MSGS="6|Starting $short...
6|$short: loaded configuration from /etc/$short/$short.conf
6|Started $short.
6|$short: listening on the configured sockets
6|$short: 3 workers ready
5|$short: reloading on SIGHUP
6|$short: configuration reloaded
4|$short: upstream 10.0.9.12:8080 slow to respond (1.4s)
6|$short: 240 requests in the last minute, 0 failed
3|$short: upstream 10.0.9.12:8080 refused the connection
6|$short: retrying against 10.0.9.13:8080
6|$short: idle, 3 workers running" ;;
esac

# The cache key contains the unit and a checksum of its messages: editing the
# tables above must miss the cache, and a file's bare existence says nothing
# about whether the run that wrote it finished.
dir=$CACHE/$(printf '%s' "$short" | tr -c 'a-zA-Z0-9._-' '_')-$(printf '%s' "$MSGS" | cksum | tr -cd '0-9')

if [ ! -f "$dir/system.journal" ]; then
	mkdir -p "$dir"
	[ -x "$REMOTE" ] || {
		echo "fake-journalctl: $REMOTE is not executable; set JOURNAL_REMOTE" >&2
		exit 1
	}
	# Build in a directory of this run's own and rename only the finished
	# journal into place: an interrupted or failed run leaves nothing for a
	# retry to trip over (journal-remote can refuse an existing output file),
	# and two concurrent runs cannot read each other's half-written input.
	# It lives outside $dir so journalctl -D never sees work in progress.
	work=$(mktemp -d "$CACHE/build.XXXXXX")
	# The trap covers the failure paths; the success path cleans up by hand,
	# because the exec below replaces the process and EXIT traps never fire.
	trap 'rm -rf "$work"' EXIT

	printf '%s\n' "$MSGS" | awk -v u="$short" -v base="$(( ($(date +%s) - 3600) * 1000000 ))" '
	BEGIN { bid = "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d" }
	{
		i++
		split($0, p, "|")
		t = base + i * 210000000
		printf "__CURSOR=s=abc;i=%x;b=%s;m=%x;t=%x;x=0\n", i, bid, i, t
		printf "__REALTIME_TIMESTAMP=%d\n__MONOTONIC_TIMESTAMP=%d\n_BOOT_ID=%s\n", t, i, bid
		printf "_SYSTEMD_UNIT=%s\nSYSLOG_IDENTIFIER=%s\n_PID=%d\nPRIORITY=%s\n", u ".service", u, 1327, p[1]
		msg = $0; sub(/^[0-9]+\|/, "", msg)
		printf "MESSAGE=%s\n\n", msg
	}' > "$work/in.export"
	# Diagnostics go through — only the chatter is dropped.
	"$REMOTE" --output="$work/system.journal" - < "$work/in.export" >/dev/null
	mv "$work/system.journal" "$dir/system.journal"
	rm -rf "$work"
	trap - EXIT
fi

exec "$REAL" -D "$dir" -q "$@"
