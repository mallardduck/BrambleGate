package gatewaydetect

import (
	"net"
	"reflect"
	"testing"

	"github.com/mallardduck/BrambleGate/model"
)

func TestDetect_RealGatewayViaMatchingInterface(t *testing.T) {
	vlans := []model.VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}
	ifaces := ifaceIPs{"eth0": {net.ParseIP("192.168.10.50")}}
	gateways := map[string]net.IP{"eth0": net.ParseIP("192.168.10.1")}

	got := detect(vlans, ifaces, gateways)
	want := map[string]string{"trusted": "192.168.10.1"}
	if !reflect.DeepEqual(got.PerVLAN, want) {
		t.Fatalf("PerVLAN = %+v, want %+v", got.PerVLAN, want)
	}
	if got.Primary != "192.168.10.1" {
		t.Fatalf("Primary = %q, want 192.168.10.1", got.Primary)
	}
}

func TestDetect_FallsBackToHeuristicWhenNoRouteMatch(t *testing.T) {
	vlans := []model.VLAN{{Name: "guest", CIDRs: []string{"192.168.20.0/24"}}}
	// No interface/gateway data at all — as on an unsupported OS
	// (routes_other.go) or a bridge-mode deployment with no per-VLAN
	// interface visible to the container.
	got := detect(vlans, ifaceIPs{}, nil)
	want := map[string]string{"guest": "192.168.20.1"}
	if !reflect.DeepEqual(got.PerVLAN, want) {
		t.Fatalf("PerVLAN = %+v, want %+v", got.PerVLAN, want)
	}
	if got.Primary != "" {
		t.Fatalf("Primary = %q, want empty (nothing found via tier 1)", got.Primary)
	}
}

func TestDetect_RealRouteWinsOverHeuristic(t *testing.T) {
	vlans := []model.VLAN{{Name: "trusted", CIDRs: []string{"10.0.0.0/24"}}}
	ifaces := ifaceIPs{"eth0": {net.ParseIP("10.0.0.5")}}
	gateways := map[string]net.IP{"eth0": net.ParseIP("10.0.0.254")}

	got := detect(vlans, ifaces, gateways)
	if got.PerVLAN["trusted"] != "10.0.0.254" {
		t.Fatalf("PerVLAN[trusted] = %q, want the real gateway 10.0.0.254 (not the .1 heuristic)", got.PerVLAN["trusted"])
	}
}

func TestDetect_HeuristicSkipsIPv6(t *testing.T) {
	vlans := []model.VLAN{{Name: "v6only", CIDRs: []string{"fd00:10::/64"}}}
	got := detect(vlans, ifaceIPs{}, nil)
	if _, ok := got.PerVLAN["v6only"]; ok {
		t.Fatalf("expected no heuristic guess for an IPv6-only VLAN, got %+v", got.PerVLAN)
	}
}

func TestDetect_MultipleVLANsIndependentGateways(t *testing.T) {
	vlans := []model.VLAN{
		{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
		{Name: "guest", CIDRs: []string{"192.168.20.0/24"}},
	}
	ifaces := ifaceIPs{
		"eth0": {net.ParseIP("192.168.10.50")},
		"eth1": {net.ParseIP("192.168.20.50")},
	}
	gateways := map[string]net.IP{
		"eth0": net.ParseIP("192.168.10.1"),
		// eth1 has no known default-route gateway — guest falls back to the
		// heuristic.
	}
	got := detect(vlans, ifaces, gateways)
	want := map[string]string{"trusted": "192.168.10.1", "guest": "192.168.20.1"}
	if !reflect.DeepEqual(got.PerVLAN, want) {
		t.Fatalf("PerVLAN = %+v, want %+v", got.PerVLAN, want)
	}
}
