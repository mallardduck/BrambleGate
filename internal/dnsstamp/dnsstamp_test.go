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
