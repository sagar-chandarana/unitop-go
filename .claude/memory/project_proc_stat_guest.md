---
name: proc-stat-guest
description: /proc/stat counts guest time twice on purpose; the man page does not say so and the kernel source is the only authority
type: project
---

The `cpu` line's fields are `user nice system idle iowait irq softirq steal
guest guest_nice`. **`guest` is already inside `user`, and `guest_nice` inside
`nice`** — the kernel adds each unit of guest cputime to both:

```c
/* kernel/sched/cputime.c, account_guest_time() */
task_group_account_field(p, CPUTIME_USER, cputime);
cpustat[CPUTIME_GUEST] += cputime;      /* the same cputime, twice */
```

So summing all ten fields double-counts guest time. It inflates the busy time
*and* the total, and since busy is the smaller of the two the reported
percentage goes **up**: a hypervisor spending half a second in a guest and half
idle reads 67%, not 50%. Only hypervisors are affected — elsewhere the fields
are zero, which is exactly why it survives casual testing.

**Where the authority is.** `proc_stat(5)` defines the fields but says nothing
about their relationship, so citing it settles nothing in either direction. The
kernel source does. `htop` and `top` both subtract guest from user for the same
reason.

**How to apply:** skip indices 8 and 9 when summing (`host.go`). Anything else
derived from `/proc/stat` needs the same care. `hostcpu_test.go` pins it with
synthetic samples, because a development machine has no guest time to observe
and the bug is invisible without one.

Related: [[journalctl-traps]], [[verification-hosts]]
