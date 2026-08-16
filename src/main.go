package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

var version = "0.2.0"

func main() {
	var (
		host     = flag.String("H", "", "monitor a remote host over ssh (e.g. root@server1)")
		interval = flag.Duration("i", time.Second, "refresh interval")
		sortFlag = flag.String("s", "cpu", "initial sort column: name|state|cpu|mem|net|io|restarts|tasks|uptime")
		reverse  = flag.Bool("r", false, "reverse the initial sort")
		showAll  = flag.Bool("a", false, "include inactive/dead units")
		tree     = flag.Bool("t", false, "start in tree view, grouped by slice")
		filter   = flag.String("f", "", "initial unit filter")
		noLogs   = flag.Bool("no-logs", false, "start with the log pane hidden")
		readOnly = flag.Bool("read-only", false, "disable the unit action menu")
		sudo     = flag.Bool("sudo", false, "run unit actions through 'sudo -n systemctl'")
		showVer  = flag.Bool("v", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("unitop", version)
		return
	}

	sortBy, ok := parseSortKey(*sortFlag)
	if !ok {
		fmt.Fprintf(os.Stderr, "unitop: unknown sort column %q\n", *sortFlag)
		os.Exit(2)
	}

	r := newRunner(*host)
	defer r.close()
	label := *host
	if label == "" {
		if h, err := os.Hostname(); err == nil {
			label = h
		} else {
			label = "localhost"
		}
	}

	m := newModel(r, label, clampInterval(*interval), sortBy, *reverse, *showAll, *tree, *filter)
	m.readOnly, m.sudo = *readOnly, *sudo
	if *noLogs {
		m.showLogs = false
	}

	p := tea.NewProgram(&m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		r.close()
		fmt.Fprintln(os.Stderr, "unitop:", err)
		os.Exit(1)
	}
}

func usage() {
	out := flag.CommandLine.Output()
	fmt.Fprintf(out, `unitop %s — a TUI for systemd service CPU, memory, network, restarts and logs.

usage: unitop [flags]

`, version)
	flag.PrintDefaults()
	fmt.Fprintf(out, `
notes:
  unitop needs systemd %d or newer on the machine it watches. It checks at
  startup and reports the version if the host is too old.

`, minSystemd)
	fmt.Fprint(out, `  NET columns need IP accounting, which is off by default on most units. Enable
  it per unit with IPAccounting=yes, or fleet-wide with DefaultIPAccounting=yes
  in /etc/systemd/system.conf.

  Reading the journal needs membership of the systemd-journal group, or root.

  Unit actions (right-click, or Enter, on a unit) run systemctl directly, so
  they need privilege: run unitop as root, or pass -sudo to route them through
  'sudo -n systemctl'. Interactive polkit is never used. -read-only removes the
  action menu entirely.

  -H runs systemctl and journalctl over ssh; key-based auth is required
  (BatchMode is on, so unitop never prompts for a password).
`)
}
