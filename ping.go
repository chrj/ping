package ping

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
	"golang.org/x/time/rate"
)

var (
	ErrUnknownReply   = errors.New("unknown reply")
	ErrCorruptedReply = errors.New("corrupted reply")
)

const (
	ProtocolICMP   = 1
	ProtocolICMPv6 = 58

	// tsLen is the size of time.Time.MarshalBinary() output.
	tsLen = 15
)

type Request struct {
	Target net.IP
	Size   int
	Count  int
	Delay  time.Duration
}

type Reply struct {
	TTL int
	RTT time.Duration
	Seq int
	Src net.IP
	Err error
}

func (r *Request) Send(ctx context.Context) (<-chan Reply, error) {
	var c *icmp.PacketConn
	var err error
	var proto int
	var pktType icmp.Type

	if r.Size < tsLen {
		r.Size = tsLen
	}

	if r.Target.To4() != nil {
		c, err = icmp.ListenPacket("udp4", "0.0.0.0")
		proto = ProtocolICMP
		pktType = ipv4.ICMPTypeEcho
	} else {
		c, err = icmp.ListenPacket("udp6", "::")
		proto = ProtocolICMPv6
		pktType = ipv6.ICMPTypeEchoRequest
	}

	if err != nil {
		return nil, err
	}

	// Set up control message flags before spawning goroutines so we can
	// return errors directly instead of panicking.
	if proto == ProtocolICMPv6 {
		pc := c.IPv6PacketConn()
		if err := pc.SetControlMessage(0xFF, true); err != nil {
			c.Close()
			return nil, err
		}
	} else {
		pc := c.IPv4PacketConn()
		if err := pc.SetControlMessage(0xFF, true); err != nil {
			c.Close()
			return nil, err
		}
	}

	rc := make(chan Reply)
	done := make(chan struct{})

	// Sender goroutine: sends r.Count echo requests, then signals done.
	go func() {
		defer close(done)

		id := os.Getpid() & 0xffff
		data := make([]byte, r.Size)
		lim := rate.NewLimiter(rate.Every(r.Delay), 1)

		for seq := 0; seq < r.Count; seq++ {
			if err := lim.Wait(ctx); err != nil {
				return
			}

			t := time.Now()
			tsb, err := t.MarshalBinary()
			if err != nil {
				continue
			}

			copy(data, tsb)

			msg := icmp.Message{
				Type: pktType,
				Code: 0,
				Body: &icmp.Echo{
					ID:   id,
					Seq:  seq,
					Data: data,
				},
			}

			mmsg, err := msg.Marshal(nil)
			if err != nil {
				continue
			}

			target := &net.UDPAddr{IP: r.Target}
			c.WriteTo(mmsg, target)
		}
	}()

	// Receiver goroutine: reads replies, closes rc when done.
	go func() {
		defer c.Close()
		defer close(rc)

		rb := make([]byte, 1500)
		received := 0
		senderDone := false

		for received < r.Count {
			// Check context cancellation.
			select {
			case <-ctx.Done():
				return
			default:
			}

			// Check if sender finished (non-blocking).
			select {
			case <-done:
				senderDone = true
			default:
			}

			// Set a read deadline so we don't block forever.
			timeout := r.Delay + time.Second
			c.SetReadDeadline(time.Now().Add(timeout))

			reply := receiveOne(c, proto, rb)

			if reply.Err != nil {
				if netErr, ok := reply.Err.(net.Error); ok && netErr.Timeout() {
					// On timeout, exit if the sender is done — remaining
					// packets are likely lost.
					if senderDone {
						return
					}
					continue
				}
			}

			rc <- reply
			received++
		}
	}()

	return rc, nil
}

// receiveOne reads a single ICMP reply from the connection.
func receiveOne(c *icmp.PacketConn, proto int, rb []byte) Reply {
	var reply Reply
	var n int
	var err error

	if proto == ProtocolICMPv6 {
		var rcm *ipv6.ControlMessage
		pc := c.IPv6PacketConn()
		n, rcm, _, err = pc.ReadFrom(rb)
		if err != nil {
			reply.Err = err
			return reply
		}
		reply.Src = rcm.Src
		reply.TTL = rcm.HopLimit
	} else {
		var rcm *ipv4.ControlMessage
		pc := c.IPv4PacketConn()
		n, rcm, _, err = pc.ReadFrom(rb)
		if err != nil {
			reply.Err = err
			return reply
		}
		reply.Src = rcm.Src
		reply.TTL = rcm.TTL
	}

	im, err := icmp.ParseMessage(proto, rb[:n])
	if err != nil {
		reply.Err = err
		return reply
	}

	switch im.Type {
	case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:
		ep, ok := im.Body.(*icmp.Echo)
		if !ok {
			reply.Err = ErrCorruptedReply
			return reply
		}
		if len(ep.Data) < tsLen {
			reply.Err = ErrCorruptedReply
			return reply
		}
		var sent time.Time
		if err := sent.UnmarshalBinary(ep.Data[:tsLen]); err != nil {
			reply.Err = ErrCorruptedReply
			return reply
		}
		reply.RTT = time.Since(sent)
		reply.Seq = ep.Seq
	default:
		reply.Err = ErrUnknownReply
	}

	return reply
}
