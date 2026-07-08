package localrecords

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

const defaultTTL = 300

func init() { plugin.Register("localrecords", setup) }

// setup parses the localrecords stanza and inserts the handler into the chain.
//
// Corefile syntax (rendered by configgen):
//
//	localrecords home.arpa [more.zones ...] {
//	    zonedata /config/.runtime/zones/records.json
//	}
//
// The referenced JSON file carries the VLAN definitions and records (with per-VLAN
// overrides); it is written by configgen before New/Reload. Loading it once, here
// at setup, is the plugin's only file read — there is no runtime refresh.
func setup(c *caddy.Controller) error {
	lr, err := parse(c)
	if err != nil {
		return plugin.Error("localrecords", err)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		lr.Next = next
		return lr
	})
	return nil
}

func parse(c *caddy.Controller) (*LocalRecords, error) {
	lr := &LocalRecords{defaultTTL: defaultTTL, records: map[string][]*record{}}
	seen := false

	for c.Next() { // "localrecords"
		if seen {
			return nil, c.Err("localrecords: multiple stanzas in one server block are not supported")
		}
		seen = true

		zones := c.RemainingArgs()
		if len(zones) == 0 {
			zones = []string{"home.arpa"}
		}
		for i, z := range zones {
			zones[i] = dns.CanonicalName(z)
		}
		lr.Zones = zones

		var zonePath string
		for c.NextBlock() {
			switch strings.ToLower(c.Val()) {
			case "zonedata":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				zonePath = c.Val()
			default:
				return nil, c.Errf("unknown property %q", c.Val())
			}
		}
		if zonePath == "" {
			return nil, c.Err("localrecords: missing 'zonedata <path>'")
		}
		if err := lr.loadZoneData(zonePath); err != nil {
			return nil, err
		}
	}
	return lr, nil
}

// loadZoneData reads and indexes the JSON zone data into the in-memory tables.
func (lr *LocalRecords) loadZoneData(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read zone data %s: %w", path, err)
	}
	var z wireZone
	if err := json.Unmarshal(raw, &z); err != nil {
		return fmt.Errorf("parse zone data %s: %w", path, err)
	}

	if z.DefaultTTL != 0 {
		lr.defaultTTL = z.DefaultTTL
	}

	for _, v := range z.VLANs {
		vm := vlanMatch{name: v.Name}
		for _, c := range v.CIDRs {
			_, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				return fmt.Errorf("zone data vlan %q cidr %q: %w", v.Name, c, err)
			}
			vm.nets = append(vm.nets, ipnet)
		}
		lr.vlans = append(lr.vlans, vm)
	}

	for _, wr := range z.Records {
		rtype, ok := recordType(wr.Type)
		if !ok {
			return fmt.Errorf("zone data record %q: unsupported type %q", wr.Name, wr.Type)
		}
		rc := &record{
			rtype:     rtype,
			def:       normalizeValue(rtype, wr.Default),
			ttl:       wr.TTL,
			overrides: map[string]override{},
		}
		for _, o := range wr.VLANOverrides {
			rc.overrides[o.VLAN] = override{
				value:    normalizeValue(rtype, o.Value),
				ttl:      o.TTL,
				nxdomain: o.NXDomain,
			}
		}
		name := dns.CanonicalName(wr.Name)
		lr.records[name] = append(lr.records[name], rc)
	}
	return nil
}

// normalizeValue canonicalizes CNAME targets to fqdn form; address values pass
// through unchanged (parsed at answer time).
func normalizeValue(rtype uint16, v string) string {
	if v == "" {
		return ""
	}
	if rtype == dns.TypeCNAME {
		return dns.CanonicalName(v)
	}
	return v
}

func recordType(s string) (uint16, bool) {
	switch strings.ToUpper(s) {
	case "A":
		return dns.TypeA, true
	case "AAAA":
		return dns.TypeAAAA, true
	case "CNAME":
		return dns.TypeCNAME, true
	default:
		return 0, false
	}
}
