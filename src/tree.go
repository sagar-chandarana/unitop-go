package main

import (
	"sort"
	"strings"
	"time"
)

type rowKind int

const (
	rowUnit rowKind = iota
	rowSlice
)

// row is one line of the table: either a service, or a slice node in tree mode
// carrying the totals of everything beneath it.
type row struct {
	kind     rowKind
	depth    int
	unit     Unit // for rowUnit; for rowSlice this is the aggregate
	slice    string
	nUnits   int
	nFailed  int
	expanded bool
	hasKids  bool
}

func (r row) key() string {
	if r.kind == rowSlice {
		return "slice:" + r.slice
	}
	return r.unit.Name
}

// sliceParent maps a slice onto its parent using systemd's naming rule:
// user-1000.slice lives under user.slice, which lives under -.slice.
func sliceParent(name string) string {
	base := strings.TrimSuffix(name, ".slice")
	if base == "" || base == "-" {
		return ""
	}
	i := strings.LastIndex(base, "-")
	if i < 0 {
		return "-.slice"
	}
	return base[:i] + ".slice"
}

// aggregate folds a set of units into a single pseudo-unit so slice rows can
// be rendered and sorted with exactly the same code as service rows.
func aggregate(name string, units []Unit) Unit {
	agg := Unit{
		Name:       name,
		Slice:      name,
		Active:     "active",
		Sub:        "slice",
		MemCurrent: 0,
		MemPeak:    0,
		Tasks:      0,
		NRestarts:  0,
		CPUNSec:    0,
		IPIn:       0,
		IPOut:      0,
		IORead:     0,
		IOWrite:    0,
	}
	var newest time.Time
	failed := 0
	for _, u := range units {
		agg.CPUPct += u.CPUPct
		agg.NetInRate += u.NetInRate
		agg.NetOutRate += u.NetOutRate
		agg.IORRate += u.IORRate
		agg.IOWRate += u.IOWRate
		agg.MemCurrent += orZero(u.MemCurrent)
		agg.Tasks += orZero(u.Tasks)
		agg.NRestarts += orZero(u.NRestarts)
		if u.HasRates {
			agg.HasRates = true
		}
		if u.IPAccount {
			agg.IPAccount = true
		}
		if u.Failed() {
			failed++
		}
		if u.ActiveSince.After(newest) {
			newest = u.ActiveSince
		}
	}
	agg.ActiveSince = newest
	if failed > 0 {
		agg.Active = "failed"
	}
	return agg
}

// buildRows turns the filtered unit set into display rows: a flat list, or a
// slice tree honouring the collapsed set.
func buildRows(units []Unit, key sortKey, reverse, tree bool, collapsed map[string]bool) []row {
	if !tree {
		sorted := append([]Unit(nil), units...)
		sortUnits(sorted, key, reverse)
		rows := make([]row, 0, len(sorted))
		for _, u := range sorted {
			rows = append(rows, row{kind: rowUnit, unit: u})
		}
		return rows
	}

	direct := map[string][]Unit{}
	for _, u := range units {
		direct[u.Slice] = append(direct[u.Slice], u)
	}

	// Materialise every ancestor so an intermediate slice with no units of its
	// own still shows up as a level in the tree.
	nodes := map[string]bool{}
	for s := range direct {
		for cur := s; cur != ""; cur = sliceParent(cur) {
			if nodes[cur] {
				break
			}
			nodes[cur] = true
		}
	}
	children := map[string][]string{}
	var roots []string
	for s := range nodes {
		p := sliceParent(s)
		if p == "" || !nodes[p] {
			roots = append(roots, s)
			continue
		}
		children[p] = append(children[p], s)
	}

	// subtree[s] is every unit at or below s, which is what the slice row shows.
	subtree := map[string][]Unit{}
	var gather func(s string) []Unit
	gather = func(s string) []Unit {
		if v, ok := subtree[s]; ok {
			return v
		}
		out := append([]Unit(nil), direct[s]...)
		for _, c := range children[s] {
			out = append(out, gather(c)...)
		}
		subtree[s] = out
		return out
	}
	for s := range nodes {
		gather(s)
	}

	// Order siblings by the same key as the units, comparing aggregates.
	orderSlices := func(names []string) {
		aggs := make([]Unit, len(names))
		for i, n := range names {
			aggs[i] = aggregate(n, subtree[n])
		}
		idx := make([]int, len(names))
		for i := range idx {
			idx[i] = i
		}
		less := lessFunc(aggs, key, reverse)
		sort.SliceStable(idx, func(a, b int) bool { return less(idx[a], idx[b]) })
		out := make([]string, len(names))
		for i, j := range idx {
			out[i] = names[j]
		}
		copy(names, out)
	}
	orderSlices(roots)
	for k := range children {
		orderSlices(children[k])
	}

	var rows []row
	var walk func(s string, depth int)
	walk = func(s string, depth int) {
		kids := children[s]
		own := append([]Unit(nil), direct[s]...)
		sortUnits(own, key, reverse)
		agg := aggregate(s, subtree[s])
		failed := 0
		for _, u := range subtree[s] {
			if u.Failed() {
				failed++
			}
		}
		expanded := !collapsed[s]
		rows = append(rows, row{
			kind: rowSlice, depth: depth, unit: agg, slice: s,
			nUnits: len(subtree[s]), nFailed: failed,
			expanded: expanded, hasKids: len(kids)+len(own) > 0,
		})
		if !expanded {
			return
		}
		for _, c := range kids {
			walk(c, depth+1)
		}
		for _, u := range own {
			rows = append(rows, row{kind: rowUnit, depth: depth + 1, unit: u})
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return rows
}
