package releaseweb

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

type client struct {
	ip          string
	bucket      string
	viaLoopback bool
	https       bool
}

// Tailscale Funnel proxies from a loopback peer with X-Forwarded-For (real client last) and
// X-Forwarded-Proto; both are trusted only from loopback, and only XFF's last value.
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
