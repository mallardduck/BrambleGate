package selfip

import (
	"net"
	"testing"

	"github.com/mallardduck/BrambleGate/model"
)

func addr(cidr string) net.Addr {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		panic(err)
	}
	ipnet.IP = ip
	return ipnet
}

func vlans() []model.VLAN {
	return []model.VLAN{
		{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
		{Name: "untrusted-wifi", CIDRs: []string{"192.168.30.0/24"}},
	}
}

func TestDetectSingleVLANMatch(t *testing.T) {
	res := Detect(vlans(), []net.Addr{addr("192.168.10.55/24")})
	if got := res.PerVLAN["trusted"].V4; got != "192.168.10.55" {
		t.Fatalf("trusted V4 = %q, want 192.168.10.55", got)
	}
	if _, ok := res.PerVLAN["untrusted-wifi"]; ok {
		t.Fatalf("untrusted-wifi should have no match: %+v", res.PerVLAN)
	}
	if res.Primary.V4 != "192.168.10.55" {
		t.Fatalf("Primary.V4 = %q, want 192.168.10.55", res.Primary.V4)
	}
}

func TestDetectMultiVLANMacvlanStyle(t *testing.T) {
	res := Detect(vlans(), []net.Addr{
		addr("192.168.10.5/24"),
		addr("192.168.30.5/24"),
	})
	if got := res.PerVLAN["trusted"].V4; got != "192.168.10.5" {
		t.Fatalf("trusted V4 = %q", got)
	}
	if got := res.PerVLAN["untrusted-wifi"].V4; got != "192.168.30.5" {
		t.Fatalf("untrusted-wifi V4 = %q", got)
	}
}

func TestDetectNoMatchAnywhere(t *testing.T) {
	// e.g. bridge-mode: the only visible address is Docker's own bridge subnet,
	// which isn't any declared VLAN's CIDR.
	res := Detect(vlans(), []net.Addr{addr("172.17.0.2/16")})
	if len(res.PerVLAN) != 0 {
		t.Fatalf("expected no VLAN matches, got %+v", res.PerVLAN)
	}
	// Primary still reports whatever was visible — callers decide whether it's
	// usable (bridge-mode's Primary is expected to be a useless bridge IP).
	if res.Primary.V4 != "172.17.0.2" {
		t.Fatalf("Primary.V4 = %q, want 172.17.0.2", res.Primary.V4)
	}
}

func TestDetectIPv6OnlyVLAN(t *testing.T) {
	v := []model.VLAN{{Name: "trusted", CIDRs: []string{"2001:db8:10::/64"}}}
	res := Detect(v, []net.Addr{addr("2001:db8:10::5/64")})
	if got := res.PerVLAN["trusted"].V6; got != "2001:db8:10::5" {
		t.Fatalf("trusted V6 = %q", got)
	}
	if res.PerVLAN["trusted"].V4 != "" {
		t.Fatalf("expected no V4 match, got %q", res.PerVLAN["trusted"].V4)
	}
}

func TestDetectExcludesLoopbackAndLinkLocal(t *testing.T) {
	res := Detect(vlans(), []net.Addr{
		addr("127.0.0.1/8"),
		addr("169.254.1.5/16"),
		addr("fe80::1/64"),
	})
	if len(res.PerVLAN) != 0 {
		t.Fatalf("expected no VLAN matches, got %+v", res.PerVLAN)
	}
	if res.Primary.V4 != "" || res.Primary.V6 != "" {
		t.Fatalf("expected no Primary, got %+v", res.Primary)
	}
}

func TestDetectLiveNeverErrors(t *testing.T) {
	// Smoke test only — real interfaces vary by host; just confirm it runs and
	// returns a non-nil PerVLAN map.
	res := DetectLive(vlans())
	if res.PerVLAN == nil {
		t.Fatal("PerVLAN should never be nil")
	}
}

func TestCandidatesFindsUndeclaredNetworks(t *testing.T) {
	// Mirrors a real macvlan-per-VLAN report: three attached networks, none
	// declared yet.
	cands := Candidates(nil, []net.Addr{
		addr("192.168.32.164/24"),
		addr("192.168.31.164/23"), // network is .30.0/23, not .31.0/24
		addr("192.168.1.164/24"),
	})
	if len(cands) != 3 {
		t.Fatalf("want 3 candidates, got %+v", cands)
	}
	byCIDR := map[string]Candidate{}
	for _, c := range cands {
		byCIDR[c.CIDR] = c
	}
	for _, want := range []string{"192.168.32.0/24", "192.168.30.0/23", "192.168.1.0/24"} {
		c, ok := byCIDR[want]
		if !ok {
			t.Fatalf("missing candidate for %s, got %+v", want, cands)
		}
		if c.Suggested == "" {
			t.Errorf("candidate %s has no suggested name", want)
		}
	}
	if got := byCIDR["192.168.30.0/23"].SampleIP; got != "192.168.31.164" {
		t.Errorf("sample IP = %q, want 192.168.31.164", got)
	}
}

func TestCandidatesExcludesAlreadyDeclaredVLANs(t *testing.T) {
	existing := []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}
	cands := Candidates(existing, []net.Addr{
		addr("192.168.10.55/24"), // already covered
		addr("192.168.30.5/24"),  // new
	})
	if len(cands) != 1 || cands[0].CIDR != "192.168.30.0/24" {
		t.Fatalf("want just the undeclared network, got %+v", cands)
	}
}

func TestCandidatesDedupesSameNetwork(t *testing.T) {
	cands := Candidates(nil, []net.Addr{
		addr("192.168.10.5/24"),
		addr("192.168.10.6/24"), // same network, second address
	})
	if len(cands) != 1 {
		t.Fatalf("want 1 deduped candidate, got %+v", cands)
	}
}

func TestCandidatesExcludesLoopbackAndLinkLocal(t *testing.T) {
	cands := Candidates(nil, []net.Addr{
		addr("127.0.0.1/8"),
		addr("169.254.1.5/16"),
	})
	if len(cands) != 0 {
		t.Fatalf("want no candidates, got %+v", cands)
	}
}

func TestCandidatesLiveNeverErrors(t *testing.T) {
	if cands := CandidatesLive(nil); cands == nil {
		// nil is a valid "found nothing" result — just confirm it doesn't panic.
		_ = cands
	}
}
