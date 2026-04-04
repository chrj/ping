package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/chrj/ping"
)

var (
	count    = flag.Int("count", 4, "Stop after sending this many requests")
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
			log.Fatalf("couldn't resolve target: %v", *target)
		}
		t = addrs[0]
	}

	r := &ping.Request{
		Target: t,
		Size:   *size,
		Count:  *count,
		Delay:  *interval,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	replies, err := r.Send(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for reply := range replies {
		if reply.Err != nil {
			log.Printf("error: %v", reply.Err)
			continue
		}
		log.Printf("reply from %v seq=%v ttl=%v rtt=%v",
			reply.Src, reply.Seq, reply.TTL, reply.RTT)
	}
}
