# ICMP echo (ping) in Go

As a small exercise, I implemented ICMP echo using the `golang.org/x/net/icmp` and `golang.org/x/net/ipv[46]` packages.

## Notes

* Timestamps are encoded in the data part of of the echo packet
* Error handling needs improvement
* Still work in progress
