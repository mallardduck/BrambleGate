// Package dnsutil is a small shared helper for checks that issue real DNS
// queries against a target (checks/protocol and checks/bramblegate both need
// this — it carries no BrambleGate-specific knowledge itself).
package dnsutil

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/miekg/dns"
)

const (
	DefaultPort    = 53
	DefaultTimeout = 5 * time.Second
)

// WithDefaultPort appends DefaultPort to addr if it has none.
func WithDefaultPort(addr string) string {
	if _, _, err := net.SplitHostPort(addr); err == nil {
		return addr
	}
	return net.JoinHostPort(addr, strconv.Itoa(DefaultPort))
}

// Query issues a single UDP query for name/qtype against addr (host or
// host:port; port defaults to 53).
func Query(addr, name string, qtype uint16) (*dns.Msg, error) {
	m := new(dns.Msg)
	m.SetQuestion(dns.Fqdn(name), qtype)
	c := &dns.Client{Timeout: DefaultTimeout}
	resp, _, err := c.Exchange(m, WithDefaultPort(addr))
	if err != nil {
		return nil, fmt.Errorf("query %s against %s: %w", name, addr, err)
	}
	return resp, nil
}

// AAnswers extracts A-record IPv4 strings from a response's answer section.
func AAnswers(resp *dns.Msg) []string {
	var out []string
	for _, rr := range resp.Answer {
		if a, ok := rr.(*dns.A); ok {
			out = append(out, a.A.String())
		}
	}
	return out
}

// Contains reports whether ips contains ip.
func Contains(ips []string, ip string) bool {
	for _, v := range ips {
		if v == ip {
			return true
		}
	}
	return false
}
