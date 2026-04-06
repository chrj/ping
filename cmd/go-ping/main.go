package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/signal"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/chrj/ping"
)

var (
	count    = flag.Int("count", 0, "Stop after sending this many requests (0 = run until quit)")
	interval = flag.Duration("interval", time.Second, "Wait between requests")
	size     = flag.Int("size", 64, "Data bytes")
	target   = flag.String("target", "", "Target host or IP address")
)

func main() {
	flag.Parse()

	if *target == "" {
		fmt.Fprintln(os.Stderr, "usage: go-ping -target <host>")
		os.Exit(1)
	}

	t := net.ParseIP(*target)
	if t == nil {
		addrs, err := net.LookupIP(*target)
		if err != nil || len(addrs) == 0 {
			fmt.Fprintf(os.Stderr, "couldn't resolve target: %v\n", *target)
			os.Exit(1)
		}
		t = addrs[0]
	}

	cnt := *count
	if cnt == 0 {
		cnt = math.MaxInt32
	}

	r := &ping.Request{
		Target: t,
		Size:   *size,
		Count:  cnt,
		Delay:  *interval,
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	replies, err := r.Send(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ping error: %v\n", err)
		os.Exit(1)
	}

	m := newModel(replies, cancel, *target, *interval)
	p := tea.NewProgram(m, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "TUI error: %v\n", err)
		os.Exit(1)
	}
}
