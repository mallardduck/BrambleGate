package localrecords

import (
	"errors"
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

const defaultTTL = 300

var errNonNumericTTL = errors.New("ttl must be a whole number of seconds")

func init() { plugin.Register("localrecords", setup) }

// setup parses the localrecords stanza and inserts the handler into the chain.
//
// Corefile syntax (rendered by configgen from records.yaml):
//
//	localrecords home.arpa [more.zones ...] {
//	    ttl 300
//	    record nas.home.arpa      A     192.168.10.20
//	    record git.home.arpa      CNAME nas.home.arpa
//	    record host.home.arpa     AAAA  fd00::1
//	}
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
	lr := &LocalRecords{TTL: defaultTTL, records: map[string][]entry{}}
	seen := false

	for c.Next() { // "localrecords"
		if seen {
			return nil, c.Err("localrecords: multiple stanzas in one server block are not supported")
		}
		seen = true

		zones := c.RemainingArgs()
		if len(zones) == 0 {
			// Default owned zone; configgen normally passes it explicitly.
			zones = []string{"home.arpa"}
		}
		for i, z := range zones {
			zones[i] = dns.CanonicalName(z)
		}
		lr.Zones = zones

		for c.NextBlock() {
			switch strings.ToLower(c.Val()) {
			case "ttl":
				if !c.NextArg() {
					return nil, c.ArgErr()
				}
				ttl, err := parseTTL(c.Val())
				if err != nil {
					return nil, c.Errf("invalid ttl %q: %v", c.Val(), err)
				}
				lr.TTL = ttl
			case "record":
				args := c.RemainingArgs()
				if len(args) != 3 {
					return nil, c.Errf("record needs NAME TYPE VALUE, got %d args", len(args))
				}
				rtype, ok := recordType(args[1])
				if !ok {
					return nil, c.Errf("unsupported record type %q (want A, AAAA, or CNAME)", args[1])
				}
				name := dns.CanonicalName(args[0])
				value := args[2]
				if rtype == dns.TypeCNAME {
					value = dns.CanonicalName(value)
				}
				if !nameInZones(name, lr.Zones) {
					return nil, c.Errf("record %q is outside the owned zone(s) %v", args[0], lr.Zones)
				}
				lr.records[name] = append(lr.records[name], entry{rtype: rtype, value: value})
			default:
				return nil, c.Errf("unknown property %q", c.Val())
			}
		}
	}
	return lr, nil
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

func nameInZones(name string, zones []string) bool {
	return plugin.Zones(zones).Matches(name) != ""
}

func parseTTL(s string) (uint32, error) {
	// Seconds only; keep it simple and explicit.
	var n uint32
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNonNumericTTL
		}
		n = n*10 + uint32(r-'0')
	}
	return n, nil
}
