//go:build linux

package gatewaydetect

import (
	"strings"
	"testing"
)

// sampleProcNetRoute mirrors real /proc/net/route output: a header line,
// two default routes (eth0 via 192.168.1.1, eth1 via 192.168.2.1 — the
// Gateway column is the IP's bytes in little-endian hex, e.g. 192.168.1.1 =
// C0.A8.01.01 -> "0101A8C0") and one non-default (on-link) route that must
// be ignored.
const sampleProcNetRoute = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	00000000	0101A8C0	0003	0	0	0	00000000	0	0	0
eth1	00000000	0102A8C0	0003	0	0	100	00000000	0	0	0
eth0	000AA8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`

func TestParseProcNetRoute(t *testing.T) {
	got := parseProcNetRoute(strings.NewReader(sampleProcNetRoute))
	if len(got) != 2 {
		t.Fatalf("got %d interfaces, want 2: %+v", len(got), got)
	}
	if got["eth0"] == nil || got["eth0"].String() != "192.168.1.1" {
		t.Fatalf("eth0 gateway = %v, want 192.168.1.1", got["eth0"])
	}
	if got["eth1"] == nil || got["eth1"].String() != "192.168.2.1" {
		t.Fatalf("eth1 gateway = %v, want 192.168.2.1", got["eth1"])
	}
}

func TestParseProcNetRoute_IgnoresNonDefaultRoutes(t *testing.T) {
	const onlyOnLink = `Iface	Destination	Gateway 	Flags	RefCnt	Use	Metric	Mask		MTU	Window	IRTT
eth0	000AA8C0	00000000	0001	0	0	0	00FFFFFF	0	0	0
`
	got := parseProcNetRoute(strings.NewReader(onlyOnLink))
	if len(got) != 0 {
		t.Fatalf("got %+v, want no default-route gateways", got)
	}
}
