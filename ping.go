package ping

import (
	"context"
	"errors"
	"log"
	"net"
	"os"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

var (
	ErrUnknownReply   = errors.New("unknown reply")
	ErrCorruptedReply = errors.New("corrupted reply")
)

const (
	ProtocolICMP   = 1
	ProtocolICMPv6 = 58
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

	rc := make(chan Reply)

	if r.Size < 15 {
		r.Size = 15
	}

	if len(r.Target) == 4 {
		c, err = icmp.ListenPacket("udp6", "::")
		proto = ProtocolICMPv6
	} else {
		c, err = icmp.ListenPacket("udp4", "0.0.0.0")
		proto = ProtocolICMP
	}

	if err != nil {
		return nil, err
	}

	go func() {

		defer c.Close()

		received := 0

		var im *icmp.Message

		for {

			sent := time.Now()

			var reply Reply
			var err error
			var n int

			rb := make([]byte, 1500)

			if proto == ProtocolICMPv6 {

				var rcm *ipv6.ControlMessage

				pc := c.IPv6PacketConn()
				if err := pc.SetControlMessage(0xFF, true); err != nil {
					panic("couldn't set ipv6 cm flags: " + err.Error())
				}

				n, rcm, _, err = pc.ReadFrom(rb)
				if err != nil {
					reply.Err = err
					goto send
				}

				reply.Src = rcm.Src
				reply.TTL = rcm.HopLimit

			} else {

				var rcm *ipv4.ControlMessage

				pc := c.IPv4PacketConn()
				if err := pc.SetControlMessage(0xFF, true); err != nil {
					panic("couldn't set ipv4 cm flags: " + err.Error())
				}

				n, rcm, _, err = pc.ReadFrom(rb)
				if err != nil {
					log.Printf("err: %v", err)
					reply.Err = err
					goto send
				}

				reply.Src = rcm.Src
				reply.TTL = rcm.TTL

			}

			im, err = icmp.ParseMessage(proto, rb[:n])
			if err != nil {
				reply.Err = err
				goto send
			}

			switch im.Type {

			case ipv4.ICMPTypeEchoReply, ipv6.ICMPTypeEchoReply:

				if ep, ok := im.Body.(*icmp.Echo); ok {

					err := sent.UnmarshalBinary(ep.Data[0:15])
					if err != nil {
						reply.Err = ErrCorruptedReply
						break
					}

					reply.RTT = time.Since(sent)
					reply.Seq = ep.Seq

					break
				}

			default:
				reply.Err = ErrUnknownReply
			}

		send:

			rc <- reply

			received += 1

			if received >= r.Count {
				close(rc)
				return
			}

		}

	}()

	go func() {

		id := os.Getpid() & 0xffff
		data := make([]byte, r.Size)

		for seq := 0; seq < r.Count; seq++ {

			t := time.Now()
			tsb, err := t.MarshalBinary()
			if err != nil {
				log.Printf("time marshal error: %v", err)
				continue
			}

			copy(data, tsb)

			msg := icmp.Message{
				Type: ipv4.ICMPTypeEcho,
				Code: 0,
				Body: &icmp.Echo{
					ID:   id,
					Seq:  seq,
					Data: data,
				},
			}

			mmsg, err := msg.Marshal(nil)
			if err != nil {
				log.Printf("icmp marshal error: %v", err)
				continue
			}

			target := &net.UDPAddr{IP: r.Target}

			if n, err := c.WriteTo(mmsg, target); err != nil {
				log.Printf("write error: %v", err)
			} else if n != len(mmsg) {
				log.Printf("incomplete write: %v", err)
			}

			time.Sleep(r.Delay)

		}

	}()

	return rc, nil

}
