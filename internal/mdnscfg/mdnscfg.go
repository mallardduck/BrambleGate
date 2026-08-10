// Package mdnscfg translates the model's mDNS settings + records into the
// mdnsbridge plugin's runtime Config. It lives on the root side (the plugin can't
// import model); both cli (at startup) and gui (on config change) use it to build
// or refresh the shared discovery Table's config.
package mdnscfg

import (
	"strings"

	"github.com/mallardduck/BrambleGate/internal/configgen"
	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsbridge"
)

// Build assembles the plugin Config: naming/auto-publish from settings, and
// promoted bindings from the type:mdns records. VLAN membership is not part
// of this — selectors read the shared vlanmatch.Current() singleton
// directly (dev-docs/query-log.md), so there's nothing to translate here.
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

func fqdn(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, ".")) + "."
}
