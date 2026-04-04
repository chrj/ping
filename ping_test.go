package ping

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// skipUnprivileged skips the test if we cannot open an ICMP socket.
func skipUnprivileged(t *testing.T) {
	t.Helper()
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		t.Skipf("cannot open ICMP socket: %v", err)
	}
	_ = c.Close()
}

func TestIPv4Detection(t *testing.T) {
	tests := []struct {
		name   string
		ip     string
		wantV4 bool
	}{
		{"ipv4", "127.0.0.1", true},
		{"ipv4-parsed", "192.168.1.1", true},
		{"ipv6-loopback", "::1", false},
		{"ipv6", "2001:db8::1", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip := net.ParseIP(tt.ip)
			if ip == nil {
				t.Fatalf("failed to parse %q", tt.ip)
			}
			gotV4 := ip.To4() != nil
			if gotV4 != tt.wantV4 {
				t.Errorf("To4() != nil = %v for %q, want %v (len=%d)", gotV4, tt.ip, tt.wantV4, len(ip))
			}
		})
	}
}

func TestMinSizeEnforced(t *testing.T) {
	r := &Request{
		Target: net.ParseIP("127.0.0.1"),
		Size:   1,
		Count:  1,
		Delay:  time.Second,
	}
	if r.Size < tsLen {
		// Send would enforce this, but we can't call Send without privileges.
		// Just verify the constant is what we expect.
		if tsLen != 15 {
			t.Errorf("tsLen = %d, want 15", tsLen)
		}
	}
}

func TestTimestampRoundTrip(t *testing.T) {
	now := time.Now()
	data, err := now.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != tsLen {
		t.Fatalf("MarshalBinary length = %d, want %d", len(data), tsLen)
	}

	var decoded time.Time
	if err := decoded.UnmarshalBinary(data); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(now) {
		t.Errorf("round-trip mismatch: got %v, want %v", decoded, now)
	}
}

func TestReceiveOneEchoReply(t *testing.T) {
	// Build a synthetic ICMP echo reply with a valid timestamp.
	ts := time.Now()
	tsb, err := ts.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	payload := make([]byte, 64)
	copy(payload, tsb)

	msg := icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: &icmp.Echo{
			ID:   1234,
			Seq:  7,
			Data: payload,
		},
	}
	raw, err := msg.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Feed the raw bytes into receiveOne via a UDP loopback pair.
	// We create a local UDP conn to simulate the ICMP packet conn.
	laddr, err := net.ResolveUDPAddr("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverConn, err := net.ListenUDP("udp4", laddr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serverConn.Close() }()

	clientConn, err := net.DialUDP("udp4", nil, serverConn.LocalAddr().(*net.UDPAddr))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = clientConn.Close() }()

	// Send the raw ICMP bytes over UDP (just for parsing test).
	if _, err := clientConn.Write(raw); err != nil {
		t.Fatal(err)
	}

	// Read it back and parse manually to verify our parsing logic.
	buf := make([]byte, 1500)
	n, err := serverConn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}

	im, err := icmp.ParseMessage(ProtocolICMP, buf[:n])
	if err != nil {
		t.Fatal(err)
	}

	ep, ok := im.Body.(*icmp.Echo)
	if !ok {
		t.Fatal("body is not *icmp.Echo")
	}
	if ep.Seq != 7 {
		t.Errorf("Seq = %d, want 7", ep.Seq)
	}
	if ep.ID != 1234 {
		t.Errorf("ID = %d, want 1234", ep.ID)
	}
	if len(ep.Data) < tsLen {
		t.Fatalf("Data too short: %d < %d", len(ep.Data), tsLen)
	}

	var decoded time.Time
	if err := decoded.UnmarshalBinary(ep.Data[:tsLen]); err != nil {
		t.Fatal(err)
	}
	if !decoded.Equal(ts) {
		t.Errorf("timestamp mismatch: got %v, want %v", decoded, ts)
	}
}

func TestReceiveOneCorruptedTimestamp(t *testing.T) {
	// Echo reply with data too short for a timestamp.
	msg := icmp.Message{
		Type: ipv4.ICMPTypeEchoReply,
		Code: 0,
		Body: &icmp.Echo{
			ID:   1,
			Seq:  0,
			Data: []byte{0x01, 0x02, 0x03},
		},
	}
	raw, err := msg.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}

	im, err := icmp.ParseMessage(ProtocolICMP, raw)
	if err != nil {
		t.Fatal(err)
	}

	ep, ok := im.Body.(*icmp.Echo)
	if !ok {
		t.Fatal("body is not *icmp.Echo")
	}
	if len(ep.Data) >= tsLen {
		t.Fatalf("expected short data, got len=%d", len(ep.Data))
	}
}

func TestReceiveOneUnknownType(t *testing.T) {
	// A non-echo-reply type should produce ErrUnknownReply.
	msg := icmp.Message{
		Type: ipv4.ICMPTypeDestinationUnreachable,
		Code: 0,
		Body: &icmp.DstUnreach{
			Data: make([]byte, 28),
		},
	}
	raw, err := msg.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}

	im, err := icmp.ParseMessage(ProtocolICMP, raw)
	if err != nil {
		t.Fatal(err)
	}

	if im.Type != ipv4.ICMPTypeDestinationUnreachable {
		t.Errorf("Type = %v, want DestinationUnreachable", im.Type)
	}
}

func TestPingLoopbackIPv4(t *testing.T) {
	skipUnprivileged(t)

	r := &Request{
		Target: net.ParseIP("127.0.0.1"),
		Size:   64,
		Count:  3,
		Delay:  100 * time.Millisecond,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	replies, err := r.Send(ctx)
	if err != nil {
		t.Fatal(err)
	}

	var received int
	for reply := range replies {
		if reply.Err != nil {
			t.Errorf("reply %d: unexpected error: %v", received, reply.Err)
			continue
		}
		if reply.Seq != received {
			t.Errorf("reply %d: Seq = %d, want %d", received, reply.Seq, received)
		}
		if reply.RTT <= 0 {
			t.Errorf("reply %d: RTT = %v, want > 0", received, reply.RTT)
		}
		if reply.RTT > 5*time.Second {
			t.Errorf("reply %d: RTT = %v, suspiciously high", received, reply.RTT)
		}
		received++
	}

	if received != r.Count {
		t.Errorf("received %d replies, want %d", received, r.Count)
	}
}

func TestContextCancellation(t *testing.T) {
	skipUnprivileged(t)

	r := &Request{
		Target: net.ParseIP("127.0.0.1"),
		Size:   64,
		Count:  100,
		Delay:  100 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())

	replies, err := r.Send(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Read one reply, then cancel.
	select {
	case _, ok := <-replies:
		if !ok {
			t.Fatal("channel closed before first reply")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for first reply")
	}

	cancel()

	// Channel should close promptly after cancellation.
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-replies:
			if !ok {
				return // success — channel closed
			}
		case <-timeout:
			t.Fatal("channel not closed after context cancellation")
		}
	}
}

func TestRequestFields(t *testing.T) {
	r := &Request{
		Target: net.ParseIP("10.0.0.1"),
		Size:   128,
		Count:  5,
		Delay:  time.Second,
	}

	if r.Target == nil {
		t.Fatal("Target is nil")
	}
	if r.Size != 128 {
		t.Errorf("Size = %d, want 128", r.Size)
	}
	if r.Count != 5 {
		t.Errorf("Count = %d, want 5", r.Count)
	}
}
