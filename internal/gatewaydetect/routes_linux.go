//go:build linux

package gatewaydetect

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

// defaultGateways reads /proc/net/route — a Linux kernel interface present
// in every deployment target this project ships to (a Linux container; see
// deploy/Dockerfile) — for each interface's default-route (0.0.0.0/0)
// gateway. Reading routes needs no special capability (unlike modifying
// them), so this works in an unprivileged container.
func defaultGateways() map[string]net.IP {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseProcNetRoute(f)
}

// parseProcNetRoute is the pure/testable core of defaultGateways — the
// kernel's exact whitespace-separated column format (documented in
// route(8)): "Iface Destination Gateway Flags RefCnt Use Metric Mask MTU
// Window IRTT", one header line then one line per route. Only the default
// route (Destination "00000000") is of interest; a real gateway there is
// what a directly-attached VLAN's own DHCP-assigned router looks like from
// inside a per-VLAN macvlan interface.
func parseProcNetRoute(r io.Reader) map[string]net.IP {
	out := map[string]net.IP{}
	sc := bufio.NewScanner(r)
	first := true
	for sc.Scan() {
		if first {
			first = false // header line
			continue
		}
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 {
			continue
		}
		iface, dest, gateway := fields[0], fields[1], fields[2]
		if dest != "00000000" {
			continue
		}
		gw := parseHexLittleEndianIPv4(gateway)
		if gw == nil || gw.IsUnspecified() {
			continue
		}
		if _, ok := out[iface]; !ok {
			out[iface] = gw
		}
	}
	return out
}

// parseHexLittleEndianIPv4 decodes /proc/net/route's hex-encoded,
// little-endian IPv4 address fields (e.g. "0110A8C0" -> 192.168.1.1).
func parseHexLittleEndianIPv4(hex string) net.IP {
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return nil
	}
	ip := make(net.IP, 4)
	binary.LittleEndian.PutUint32(ip, uint32(v))
	return ip
}
