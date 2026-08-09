package dnsstamp

import (
	"testing"

	"github.com/jedisct1/go-dnsstamps"
)

func TestDoHStampRoundTrips(t *testing.T) {
	s := DoH("dns.example.com", 443)
	got, err := dnsstamps.NewServerStampFromString(s)
	if err != nil {
		t.Fatalf("stamp %q did not parse: %v", s, err)
	}
	if got.Proto != dnsstamps.StampProtoTypeDoH {
		t.Fatalf("proto = %v, want DoH", got.Proto)
	}
	if got.ProviderName != "dns.example.com" {
		t.Fatalf("provider name = %q, want dns.example.com (default port omitted)", got.ProviderName)
	}
	if got.Path != "/dns-query" {
		t.Fatalf("path = %q, want /dns-query", got.Path)
	}
	if got.ServerAddrStr != "" {
		t.Fatalf("addr = %q, want empty (no pinned IP)", got.ServerAddrStr)
	}
	if len(got.Hashes) != 0 {
		t.Fatalf("hashes = %v, want none (no cert pinning)", got.Hashes)
	}
}

func TestDoHStampIncludesNonDefaultPort(t *testing.T) {
	s := DoH("dns.example.com", 8443)
	got, err := dnsstamps.NewServerStampFromString(s)
	if err != nil {
		t.Fatalf("stamp %q did not parse: %v", s, err)
	}
	if got.ProviderName != "dns.example.com:8443" {
		t.Fatalf("provider name = %q, want dns.example.com:8443", got.ProviderName)
	}
}

func TestDoTStampRoundTrips(t *testing.T) {
	s := DoT("dns.example.com", 853)
	got, err := dnsstamps.NewServerStampFromString(s)
	if err != nil {
		t.Fatalf("stamp %q did not parse: %v", s, err)
	}
	if got.Proto != dnsstamps.StampProtoTypeTLS {
		t.Fatalf("proto = %v, want DoT", got.Proto)
	}
	if got.ProviderName != "dns.example.com" {
		t.Fatalf("provider name = %q, want dns.example.com (default port omitted)", got.ProviderName)
	}
	if got.ServerAddrStr != "" {
		t.Fatalf("addr = %q, want empty (no pinned IP)", got.ServerAddrStr)
	}
}

func TestDoTStampIncludesNonDefaultPort(t *testing.T) {
	s := DoT("dns.example.com", 8853)
	got, err := dnsstamps.NewServerStampFromString(s)
	if err != nil {
		t.Fatalf("stamp %q did not parse: %v", s, err)
	}
	if got.ProviderName != "dns.example.com:8853" {
		t.Fatalf("provider name = %q, want dns.example.com:8853", got.ProviderName)
	}
}

func TestEmptyDomainYieldsNoStamp(t *testing.T) {
	if s := DoH("", 443); s != "" {
		t.Fatalf("DoH with no domain = %q, want empty", s)
	}
	if s := DoT("", 853); s != "" {
		t.Fatalf("DoT with no domain = %q, want empty", s)
	}
}

func TestDoHURL(t *testing.T) {
	if got, want := DoHURL("dns.example.com", 443), "https://dns.example.com/dns-query"; got != want {
		t.Fatalf("DoHURL default port = %q, want %q", got, want)
	}
	if got, want := DoHURL("dns.example.com", 8443), "https://dns.example.com:8443/dns-query"; got != want {
		t.Fatalf("DoHURL non-default port = %q, want %q", got, want)
	}
	if s := DoHURL("", 443); s != "" {
		t.Fatalf("DoHURL with no domain = %q, want empty", s)
	}
}

func TestDoTURL(t *testing.T) {
	if got, want := DoTURL("dns.example.com", 853), "tls://dns.example.com"; got != want {
		t.Fatalf("DoTURL default port = %q, want %q", got, want)
	}
	if got, want := DoTURL("dns.example.com", 8853), "tls://dns.example.com:8853"; got != want {
		t.Fatalf("DoTURL non-default port = %q, want %q", got, want)
	}
	if s := DoTURL("", 853); s != "" {
		t.Fatalf("DoTURL with no domain = %q, want empty", s)
	}
}

func TestDoQURL(t *testing.T) {
	if got, want := DoQURL("dns.example.com", 853), "quic://dns.example.com"; got != want {
		t.Fatalf("DoQURL default port = %q, want %q", got, want)
	}
	if got, want := DoQURL("dns.example.com", 8853), "quic://dns.example.com:8853"; got != want {
		t.Fatalf("DoQURL non-default port = %q, want %q", got, want)
	}
	if s := DoQURL("", 853); s != "" {
		t.Fatalf("DoQURL with no domain = %q, want empty", s)
	}
}
