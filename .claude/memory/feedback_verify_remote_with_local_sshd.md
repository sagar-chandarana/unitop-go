---
name: verify-remote-with-local-sshd
description: -H can be exercised without a second machine; run your own sshd rather than reasoning about ssh argument handling
type: feedback
---

The remote path used to get read rather than run, because testing it looked
like it needed another host. It does not: `docs/helpers/local-sshd.sh start` puts a
throwaway sshd on 127.0.0.1:2222 with its own host key, and a wrapper on `PATH`
that adds only the connection details — so **unitop's real argument list is
what ssh receives**.

**Why:** an agent reported that the `--` after the host in
`ssh <opts> host -- <cmd>` was invalid and broke the remote command. The
synopsis (`destination [command ...]`) genuinely looks like it would, and
whether it does depends on which getopt the client was built against, so
reading cannot settle it. Running it can: getopt consumes the separator, the
remote command arrives intact, and unitop polled the test host normally. The
report was wrong, and would have cost a pointless change.

**The trap in the rig itself:** without `SetEnv PATH=…` in the sshd config the
session is not a login shell, has no `systemctl`, and unitop reports — quite
correctly — that the host is not running systemd. Half an hour went into that
before the rig was right.

**How to apply:** before changing anything in `runner.command`, `sshOpts` or
the poll one-liner, bring the rig up and watch the invocation log
(`docs/helpers/local-sshd.sh log`). Claims about ssh's argument handling are cheap to
test now and expensive to get wrong — quoting a journal cursor containing `;`,
or a `-g` pattern containing a space, both go through this path.

For ssh option questions, the durable order is: read the docs, then resolve
the EFFECTIVE config with `ssh -G` (it proved command-line `-T` beats a
user's `RequestTTY=force` — `requesttty false` vs `force` — without
connecting anywhere), then exercise the local sshd for the behaviour itself.

Related: [[remote-poll]], [[journalctl-traps]]
