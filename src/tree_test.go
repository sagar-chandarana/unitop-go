package main

import (
	"strings"
	"testing"
	"time"
)

func TestSliceParent(t *testing.T) {
	cases := map[string]string{
		"system.slice":       "-.slice",
		"user.slice":         "-.slice",
		"user-1000.slice":    "user.slice",
		"machine-qemu.slice": "machine.slice",
		"-.slice":            "",
	}
	for in, want := range cases {
		if got := sliceParent(in); got != want {
			t.Errorf("sliceParent(%q) = %q, want %q", in, got, want)
		}
	}
}

func treeUnits() []Unit {
	return []Unit{
		{Name: "nginx.service", Slice: "system.slice", Active: "active", Sub: "running",
			CPUPct: 10, MemCurrent: 100, Tasks: 2, NRestarts: 1},
		{Name: "redis.service", Slice: "system.slice", Active: "active", Sub: "running",
			CPUPct: 5, MemCurrent: 50, Tasks: 1, NRestarts: 0},
		{Name: "shell.service", Slice: "user-1000.slice", Active: "failed", Sub: "failed",
			CPUPct: 1, MemCurrent: 25, Tasks: unsetU64, NRestarts: unsetU64},
	}
}

func TestBuildRowsFlat(t *testing.T) {
	rows := buildRows(treeUnits(), sortCPU, false, false, nil)
	if len(rows) != 3 {
		t.Fatalf("flat mode should give one row per unit: %v", rowNames(rows))
	}
	for _, r := range rows {
		if r.kind != rowUnit || r.depth != 0 {
			t.Errorf("flat mode produced a non-unit row: %+v", r)
		}
	}
	if rows[0].unit.Name != "nginx.service" {
		t.Errorf("flat mode ignored the sort: %v", rowNames(rows))
	}
}

func TestBuildRowsTreeNestsAndAggregates(t *testing.T) {
	rows := buildRows(treeUnits(), sortCPU, false, true, map[string]bool{})
	got := rowNames(rows)
	// -.slice > system.slice > {nginx, redis}, and -.slice > user.slice >
	// user-1000.slice > shell.
	for _, want := range []string{"[/]", " [system]", "  nginx", "  redis", " [user]", "  [user-1000]", "   shell"} {
		if !strings.Contains(got, want) {
			t.Errorf("tree is missing %q:\n%v", want, got)
		}
	}

	var root row
	for _, r := range rows {
		if r.kind == rowSlice && r.slice == "-.slice" {
			root = r
		}
	}
	if root.nUnits != 3 {
		t.Errorf("root slice should count every descendant unit, got %d", root.nUnits)
	}
	if root.nFailed != 1 {
		t.Errorf("root slice should count descendant failures, got %d", root.nFailed)
	}
	if root.unit.CPUPct != 16 {
		t.Errorf("root CPU should sum descendants, got %v", root.unit.CPUPct)
	}
	if root.unit.MemCurrent != 175 {
		t.Errorf("root MEM should sum descendants, got %v", root.unit.MemCurrent)
	}
	// The unset sentinels must not poison the totals.
	if root.unit.Tasks != 3 || root.unit.NRestarts != 1 {
		t.Errorf("unset counters leaked into the aggregate: tasks=%d restarts=%d",
			root.unit.Tasks, root.unit.NRestarts)
	}
	if !root.unit.Failed() {
		t.Error("a slice containing a failed unit should read as failed")
	}
}

func TestBuildRowsTreeRespectsCollapse(t *testing.T) {
	rows := buildRows(treeUnits(), sortCPU, false, true, map[string]bool{"system.slice": true})
	got := rowNames(rows)
	if strings.Contains(got, "nginx") || strings.Contains(got, "redis") {
		t.Errorf("collapsed slice still shows its units: %v", got)
	}
	if !strings.Contains(got, "[system]") {
		t.Errorf("collapsed slice row itself disappeared: %v", got)
	}
	for _, r := range rows {
		if r.slice == "system.slice" && r.expanded {
			t.Error("collapsed slice reported as expanded")
		}
	}
}

func TestTreeSiblingsFollowTheSortKey(t *testing.T) {
	// user-1000 has less CPU than system, so system comes first by CPU and
	// last when reversed.
	rows := buildRows(treeUnits(), sortCPU, false, true, map[string]bool{"system.slice": true, "user.slice": true})
	var order []string
	for _, r := range rows {
		if r.kind == rowSlice && r.depth == 1 {
			order = append(order, sliceLabel(r.slice))
		}
	}
	if len(order) != 2 || order[0] != "system" {
		t.Errorf("slices should sort by aggregate CPU: %v", order)
	}
	rows = buildRows(treeUnits(), sortCPU, true, true, map[string]bool{"system.slice": true, "user.slice": true})
	order = order[:0]
	for _, r := range rows {
		if r.kind == rowSlice && r.depth == 1 {
			order = append(order, sliceLabel(r.slice))
		}
	}
	if len(order) != 2 || order[0] != "user" {
		t.Errorf("reversed slice order: %v", order)
	}
}

func TestTreeCursorAndCollapseKeys(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, true, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = treeUnits()
	m.rebuild()

	// Land on system.slice and collapse it with left.
	idx := -1
	for i, r := range m.rows {
		if r.kind == rowSlice && r.slice == "system.slice" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("no system.slice row: %v", rowNames(m.rows))
	}
	m.cursor = idx
	m.afterCursorMove()
	m.listKey("left")
	if !m.collapsed["system.slice"] {
		t.Error("left did not collapse the slice")
	}
	if strings.Contains(rowNames(m.rows), "nginx") {
		t.Errorf("units still listed after collapse: %v", rowNames(m.rows))
	}
	m.listKey("right")
	if m.collapsed["system.slice"] {
		t.Error("right did not expand the slice")
	}
}

func TestSelectedUnitIgnoresSliceRows(t *testing.T) {
	m := newModel(runner{}, "h", time.Second, sortCPU, false, false, true, "")
	m.width, m.height, m.ready = 140, 30, true
	m.connected = true // these fixtures stand in for a model that has already polled
	m.units = treeUnits()
	m.rebuild()
	m.cursor = 0 // the root slice
	if _, ok := m.selectedUnit(); ok {
		t.Error("a slice row must not be reported as a unit")
	}
	// And no journal should be started for it.
	if cmd := m.syncJournal(); cmd != nil {
		t.Error("syncJournal started a stream for a slice row")
	}
}
