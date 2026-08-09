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
