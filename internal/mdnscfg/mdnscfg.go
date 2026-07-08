// Package mdnscfg translates the model's mDNS settings + records into the
// mdnsbridge plugin's runtime Config. It lives on the root side (the plugin can't
// import model); both cli (at startup) and gui (on config change) use it to build
// or refresh the shared discovery Table's config.
package mdnscfg

import (
	"net"
	"strings"

	"github.com/mallardduck/BrambleGate/configgen"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
)

// Build assembles the plugin Config: naming/auto-publish/vlans from settings, and
// promoted bindings from the type:mdns records.
func Build(s model.Settings, rs model.RecordSet) mdnsbridge.Config {
	suffix := s.MDNS.Suffix
	if suffix == "" {
		suffix = configgen.OwnedZone
	}
	cfg := mdnsbridge.Config{
		DefaultSuffix: strings.TrimSuffix(suffix, "."),
		AutoPublish:   toSelectors(s.MDNS.AutoPublish),
		Naming:        toNaming(s.MDNS.Naming),
		Promoted:      map[string]mdnsbridge.Selector{},
		VLANs:         toVLANs(s.VLANs),
	}
	for _, r := range rs.Records {
		if r.IsMDNS() && r.Match != nil {
			cfg.Promoted[fqdn(r.Name)] = toSelector(*r.Match)
		}
	}
	return cfg
}

func toSelector(s model.Selector) mdnsbridge.Selector {
	return mdnsbridge.Selector{
		Service:  s.Service,
		Instance: s.Instance,
		Host:     s.Host,
		TXT:      s.TXT,
		VLAN:     s.VLAN,
		Family:   s.Family,
	}
}

func toSelectors(in []model.Selector) mdnsbridge.SelectorSet {
	if len(in) == 0 {
		return nil
	}
	out := make(mdnsbridge.SelectorSet, len(in))
	for i, s := range in {
		out[i] = toSelector(s)
	}
	return out
}

func toNaming(in []model.NamingRule) []mdnsbridge.NamingRule {
	if len(in) == 0 {
		return nil
	}
	out := make([]mdnsbridge.NamingRule, len(in))
	for i, r := range in {
		out[i] = mdnsbridge.NamingRule{Match: toSelector(r.Match), Suffix: strings.TrimSuffix(r.Suffix, ".")}
	}
	return out
}

func toVLANs(vlans []model.VLAN) map[string][]*net.IPNet {
	if len(vlans) == 0 {
		return nil
	}
	out := make(map[string][]*net.IPNet, len(vlans))
	for _, v := range vlans {
		for _, c := range v.CIDRs {
			if _, n, err := net.ParseCIDR(c); err == nil { // already validated by configgen
				out[v.Name] = append(out[v.Name], n)
			}
		}
	}
	return out
}

func fqdn(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "."
}
