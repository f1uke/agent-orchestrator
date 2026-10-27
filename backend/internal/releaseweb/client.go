package releaseweb

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// client is who sent a request, as far as the release web can tell.
type client struct {
	// ip is the client address written to the access log.
	ip string
	// bucket keys the sign-in rate limit: the address, or its /64 for IPv6,
	// since one host commonly holds a whole /64.
	bucket string
	// viaLoopback means the TCP peer is this machine, so its forwarding
	// headers are trusted.
	viaLoopback bool
	https       bool
}

// clientOf reads the client from the TCP peer. Tailscale Funnel terminates TLS
// in tailscaled and proxies to this loopback port, so its requests arrive from
// a loopback peer carrying X-Forwarded-For (the real client, appended as the
// last hop) and X-Forwarded-Proto: https. Those headers are honoured only from
// a loopback peer, and only XFF's last value, the one the local proxy wrote:
// earlier values come from the internet. A local browser sends no XFF and
// counts as 127.0.0.1.
func clientOf(r *http.Request) client {
	peer := parseAddr(r.RemoteAddr)
	c := client{viaLoopback: peer.IsValid() && peer.IsLoopback(), https: r.TLS != nil}
	addr := peer
	if c.viaLoopback {
		if fwd, ok := lastForwardedFor(r.Header); ok {
			addr = fwd
		}
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			c.https = true
		}
	}
	if !addr.IsValid() {
		c.ip, c.bucket = "unknown", "unknown"
		return c
	}
	c.ip = addr.String()
	c.bucket = c.ip
	if addr.Is6() && !addr.Is4In6() {
		c.bucket = netip.PrefixFrom(addr, 64).Masked().String()
	}
	return c
}

func parseAddr(remote string) netip.Addr {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

func lastForwardedFor(h http.Header) (netip.Addr, bool) {
	values := h.Values("X-Forwarded-For")
	if len(values) == 0 {
		return netip.Addr{}, false
	}
	hops := strings.Split(values[len(values)-1], ",")
	a := parseAddr(strings.TrimSpace(hops[len(hops)-1]))
	return a, a.IsValid()
}
