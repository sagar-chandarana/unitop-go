---
name: program-work-ownership
description: Bubble Tea never waits Cmd goroutines — own their children with begin-INSIDE-the-closure plus a mutex-gated closing flag and WaitGroup, so Add can never race Wait
type: project
---

Bubble Tea runs every `tea.Cmd` in a goroutine it never waits for, so any
external child a Cmd launches is orphaned at exit unless the program owns
it. The reusable protocol (`progWork` in `src/owner.go`):

- **begin() runs INSIDE the Cmd closure, immediately before the external
  work — never at construction.** A Cmd bubbletea drops on the floor must
  not hold shutdown hostage; a constructed-but-never-scheduled registration
  would deadlock the Wait forever.
- **One mutex covers the gate and the count.** begin refuses when closing,
  else Adds before unlocking; shutdown marks closing and cancels before
  unlocking, then Waits. A late Cmd either registered before the Wait began
  or launches nothing — `Add` can never race `Wait`, which is the
  WaitGroup misuse the race detector otherwise finds for you in production.
- Derive every timeout from the owner's root context, not Background; the
  teardown order is: drain this owner, then the stream owner, and only
  then tear down shared transport (the ssh mux) beneath them.

Regressions: `src/owner_test.go`. From TODO UT-031 (2026-08 review).

Related: [[journal-ownership-on-exit]]
