package main

import (
	"context"
	"sync"
)

// progWork owns every child the poll and action Cmds launch — systemctl,
// sudo, or the ssh carrying them. The journal streams have their own owner;
// this is everything else Bubble Tea runs in Cmd goroutines and never waits
// for.
//
// The protocol is deliberate: begin is called INSIDE a Cmd's closure,
// immediately before the external work — registering at construction would
// deadlock shutdown forever if Bubble Tea never scheduled the Cmd. Under
// one mutex, begin refuses once closing or Adds before unlocking; shutdown
// marks closing and cancels before unlocking, then Waits. A late Cmd
// therefore either registered before the Wait began, or launches nothing at
// all — Add can never race Wait.
type progWork struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	closing bool
	wg      sync.WaitGroup
}

func newProgWork() *progWork {
	ctx, cancel := context.WithCancel(context.Background())
	return &progWork{ctx: ctx, cancel: cancel}
}

// begin registers one unit of external work. ok=false means shutdown has
// begun: launch nothing. Otherwise derive every timeout from the returned
// root and call done when the work — child reaped included — is finished.
func (w *progWork) begin() (context.Context, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closing {
		return nil, false
	}
	w.wg.Add(1)
	return w.ctx, true
}

func (w *progWork) done() { w.wg.Done() }

// shutdown closes the gate, cancels every child, and returns only when all
// registered work has finished. Idempotent: later calls find the gate
// closed and an empty wait.
func (w *progWork) shutdown() {
	w.mu.Lock()
	w.closing = true
	w.cancel() // inside the gate: no closing-but-not-yet-cancelled window
	w.mu.Unlock()
	w.wg.Wait()
}
