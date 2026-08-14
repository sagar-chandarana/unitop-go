package main

import (
	"sort"
	"strings"
)

type sortKey int

const (
	sortName sortKey = iota
	sortState
	sortCPU
	sortMem
	sortNetIn
	sortNetOut
	sortIORead
	sortIOWrite
	sortRestarts
	sortTasks
	sortUptime
)

var allSortKeys = []sortKey{
	sortName, sortState, sortCPU, sortMem, sortNetIn, sortNetOut,
	sortIORead, sortIOWrite, sortRestarts, sortTasks, sortUptime,
}

func (s sortKey) String() string {
	switch s {
	case sortName:
		return "name"
	case sortState:
		return "state"
	case sortCPU:
		return "cpu"
	case sortMem:
		return "mem"
	case sortNetIn:
		return "net-in"
	case sortNetOut:
		return "net-out"
	case sortIORead:
		return "io-read"
	case sortIOWrite:
		return "io-write"
	case sortRestarts:
		return "restarts"
	case sortTasks:
		return "tasks"
	case sortUptime:
		return "uptime"
	}
	return "?"
}

// parseSortKey accepts the canonical names plus the obvious shorthands.
func parseSortKey(s string) (sortKey, bool) {
	switch strings.ToLower(s) {
	case "net", "netin", "net-in":
		return sortNetIn, true
	case "netout", "net-out":
		return sortNetOut, true
	case "io", "ioread", "io-read":
		return sortIORead, true
	case "iowrite", "io-write":
		return sortIOWrite, true
	}
	for _, k := range allSortKeys {
		if k.String() == strings.ToLower(s) {
			return k, true
		}
	}
	return sortName, false
}

// value returns the number a numeric sort column compares on, with systemd's
// "not tracked" sentinel folded to zero so those units sink to the bottom.
func (s sortKey) value(u Unit) float64 {
	switch s {
	case sortCPU:
		return u.CPUPct
	case sortMem:
		return float64(orZero(u.MemCurrent))
	case sortNetIn:
		return u.NetInRate
	case sortNetOut:
		return u.NetOutRate
	case sortIORead:
		return u.IORRate
	case sortIOWrite:
		return u.IOWRate
	case sortRestarts:
		return float64(orZero(u.NRestarts))
	case sortTasks:
		return float64(orZero(u.Tasks))
	case sortUptime:
		if u.ActiveSince.IsZero() {
			return 0
		}
		return float64(u.ActiveSince.Unix())
	}
	return 0
}

func (s sortKey) numeric() bool {
	return s != sortName && s != sortState
}

func orZero(v uint64) uint64 {
	if v == unsetU64 {
		return 0
	}
	return v
}

// stateRank orders states by how much they want attention.
func stateRank(u Unit) int {
	switch u.Active {
	case "failed":
		return 0
	case "activating", "deactivating", "reloading":
		return 1
	case "active":
		if u.Sub == "running" {
			return 2
		}
		return 3
	case "inactive":
		switch {
		case u.Skipped():
			return 7 // deliberately not run: the least interesting row there is
		case u.Result != "" && u.Result != "success":
			return 4
		case u.Ran():
			return 5 // ran to completion
		}
		return 6 // never started
	}
	return 8
}

// sortUnits orders in place in each column's natural direction — busiest
// first for the numeric columns, alphabetical for name, most-alarming first
// for state — with ties always broken by name so the list never jitters.
// reverse flips whichever order that was.
func sortUnits(us []Unit, key sortKey, reverse bool) {
	sort.SliceStable(us, lessFunc(us, key, reverse))
}

func lessFunc(us []Unit, key sortKey, reverse bool) func(i, j int) bool {
	natural := func(i, j int) bool {
		a, b := us[i], us[j]
		switch {
		case key.numeric():
			if x, y := key.value(a), key.value(b); x != y {
				return x > y
			}
		case key == sortState:
			if x, y := stateRank(a), stateRank(b); x != y {
				return x < y
			}
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	}
	if reverse {
		return func(i, j int) bool { return natural(j, i) }
	}
	return natural
}
