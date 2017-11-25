package main

import (
	"context"
	"flag"
	"log"
	"net"
	"time"

	"github.com/chrj/ping"
)

var (
	count    = flag.Int("count", 4, "Stop after sending this many requests")
	interval = flag.Duration("interval", time.Second, "Wait between requests")
	size     = flag.Int("size", 64, "Data bytes")
)

func main() {

	flag.Parse()

	r := &ping.Request{
		Target: net.ParseIP("5.79.70.137"),
		Size:   *size,
		Count:  *count,
		Delay:  *interval,
	}

	ctx := context.Background()

	replies, err := r.Send(ctx)
	if err != nil {
		log.Fatal(err)
	}

	for reply := range replies {
		log.Printf("reply from %v seq=%v ttl=%v rtt=%v",
			reply.Src, reply.Seq, reply.TTL, reply.RTT)
	}

}
