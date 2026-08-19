#!/usr/bin/env bash
# Stand up a throwaway sshd on 127.0.0.1:2222 so `unitop -H` can be exercised
# without a second machine — the remote path is otherwise the least tested part
# of the program, and reasoning about ssh's argument handling is no substitute
# for watching it.
#
#   docs/helpers/local-sshd.sh start          # then: eval "$(docs/helpers/local-sshd.sh env)"
#   unitop -H "$USER@127.0.0.1"       # runs over real ssh, against this host
#   docs/helpers/local-sshd.sh stop
#
# It runs as you, on a high port, with its own host key and authorized_keys, and
# touches nothing in ~/.ssh. The wrapper it puts on PATH injects the port and
# identity, so unitop's own argument list is what actually goes to ssh — which
# is the point. That is how the post-host `--` question was settled: OpenSSH's
# getopt consumes the separator and the remote command arrives intact.
set -euo pipefail

DIR=${UNITOP_SSHD_DIR:-${TMPDIR:-/tmp}/unitop-sshd}
SSHD=${SSHD:-/run/current-system/sw/bin/sshd}
SSH=${SSH:-/run/current-system/sw/bin/ssh}
PORT=${PORT:-2222}

start() {
	mkdir -p "$DIR/bin"
	[ -f "$DIR/hostkey" ] || ssh-keygen -q -t ed25519 -f "$DIR/hostkey" -N ''
	[ -f "$DIR/id" ] || ssh-keygen -q -t ed25519 -f "$DIR/id" -N ''
	cp "$DIR/id.pub" "$DIR/authorized_keys"
	chmod 600 "$DIR/authorized_keys" "$DIR/hostkey" "$DIR/id"

	# SetEnv matters: the session is not a login shell, so without it there is
	# no systemctl on PATH and unitop rightly reports a host that is not
	# running systemd — a confusing way to discover your own test rig is wrong.
	cat > "$DIR/sshd_config" <<-EOF
		Port $PORT
		ListenAddress 127.0.0.1
		HostKey $DIR/hostkey
		AuthorizedKeysFile $DIR/authorized_keys
		StrictModes no
		UsePAM no
		PasswordAuthentication no
		PidFile $DIR/sshd.pid
		SetEnv PATH=$PATH
	EOF

	cat > "$DIR/bin/ssh" <<-EOF
		#!/bin/sh
		# Log what unitop asked for, then add only the connection details.
		echo "ssh \$*" >> $DIR/invocations.log
		exec $SSH -p $PORT -i $DIR/id -F /dev/null \\
		  -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \\
		  -o IdentitiesOnly=yes "\$@"
	EOF
	chmod +x "$DIR/bin/ssh"

	"$SSHD" -f "$DIR/sshd_config" -D -e > "$DIR/sshd.log" 2>&1 &
	sleep 1
	PATH="$DIR/bin:$PATH" "$DIR/bin/ssh" -o BatchMode=yes "$USER@127.0.0.1" -- \
		'systemctl --version | head -1' || {
		echo "could not reach the test sshd; see $DIR/sshd.log" >&2
		exit 1
	}
	echo "up. eval \"\$($0 env)\" then: unitop -H $USER@127.0.0.1" >&2
}

case "${1:-start}" in
start) start ;;
env) echo "export PATH=$DIR/bin:\$PATH" ;;
log) tail -f "$DIR/invocations.log" ;;
stop)
	[ -f "$DIR/sshd.pid" ] && kill "$(cat "$DIR/sshd.pid")" 2>/dev/null || true
	rm -rf "$DIR"
	echo "stopped" >&2
	;;
*)
	echo "usage: $0 {start|env|log|stop}" >&2
	exit 2
	;;
esac
