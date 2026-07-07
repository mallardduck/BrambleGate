package configgen

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/mallardduck/BrambleDNS/model"
)

// Validate checks the model is renderable and internally consistent before any
// Corefile is produced. It fails loudly so a bad edit is rejected at save time
// rather than surfacing as an opaque CoreDNS parse error on reload.
func Validate(s model.Settings, rs model.RecordSet) error {
	if err := validateListeners(s.Listeners); err != nil {
		return err
	}
	if err := validateUpstream(s.UpstreamDNS); err != nil {
		return err
	}
	if err := validateVLANs(s.VLANs); err != nil {
		return err
	}
	if s.EncryptedListenerEnabled() && strings.TrimSpace(s.ACME.Domain) == "" {
		return fmt.Errorf("acme.domain is required when an encrypted listener (DoT/DoH/DoQ) is enabled")
	}
	return validateRecords(rs, s.VLANs)
}

func validateListeners(l model.Listeners) error {
	any := false
	for name, ln := range map[string]model.Listener{
		"plain": l.Plain, "dot": l.DoT, "doh": l.DoH, "doq": l.DoQ,
	} {
		if !ln.Enabled {
			continue
		}
		any = true
		if ln.Port <= 0 || ln.Port > 65535 {
			return fmt.Errorf("listeners.%s.port %d is out of range 1-65535", name, ln.Port)
		}
	}
	if !any {
		return fmt.Errorf("no listeners enabled: enable at least one of plain/dot/doh/doq")
	}
	return nil
}

func validateUpstream(u model.UpstreamTarget) error {
	if strings.TrimSpace(u.Address) == "" {
		return fmt.Errorf("upstream_dns.address is required")
	}
	host, port, err := net.SplitHostPort(u.Address)
	if err != nil {
		return fmt.Errorf("upstream_dns.address %q must be host:port: %w", u.Address, err)
	}
	if host == "" {
		return fmt.Errorf("upstream_dns.address %q is missing a host", u.Address)
	}
	if _, err := strconv.Atoi(port); err != nil {
		return fmt.Errorf("upstream_dns.address %q has a non-numeric port", u.Address)
	}
	switch u.Protocol {
	case "", "plain", "dot", "doh":
	default:
		return fmt.Errorf("upstream_dns.protocol %q must be plain, dot, or doh", u.Protocol)
	}
	return nil
}

func validateVLANs(vlans []model.VLAN) error {
	var nets []*net.IPNet
	seen := map[string]bool{}
	for _, v := range vlans {
		if strings.TrimSpace(v.Name) == "" {
			return fmt.Errorf("a vlan is missing a name")
		}
		if seen[v.Name] {
			return fmt.Errorf("duplicate vlan name %q", v.Name)
		}
		seen[v.Name] = true

		_, ipnet, err := net.ParseCIDR(v.CIDR)
		if err != nil {
			return fmt.Errorf("vlan %q cidr %q is invalid: %w", v.Name, v.CIDR, err)
		}
		for i, other := range nets {
			if netsOverlap(ipnet, other) {
				return fmt.Errorf("vlan %q cidr %q overlaps vlan %q cidr %q", v.Name, v.CIDR, vlans[i].Name, vlans[i].CIDR)
			}
		}
		nets = append(nets, ipnet)
	}
	return nil
}

func netsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

func validateRecords(rs model.RecordSet, vlans []model.VLAN) error {
	vlanNames := map[string]bool{}
	for _, v := range vlans {
		vlanNames[v.Name] = true
	}
	seen := map[string]bool{}

	for _, r := range rs.Records {
		name := r.NormalizedName()
		if strings.TrimSpace(r.Name) == "" {
			return fmt.Errorf("a record is missing a name")
		}
		if !strings.HasSuffix(name, "."+OwnedZone+".") && name != OwnedZone+"." {
			return fmt.Errorf("record %q is outside the owned zone %q", r.Name, OwnedZone)
		}

		switch r.Type {
		case model.TypeA, model.TypeAAAA, model.TypeCNAME:
		default:
			return fmt.Errorf("record %q has unsupported type %q", r.Name, r.Type)
		}

		key := name + "/" + string(r.Type)
		if seen[key] {
			return fmt.Errorf("duplicate record for %q type %s", r.Name, r.Type)
		}
		seen[key] = true

		if r.Default == "" && len(r.VLANOverrides) == 0 {
			return fmt.Errorf("record %q has no default and no vlan_overrides", r.Name)
		}
		if r.Default != "" {
			if err := validateValue(r.Type, r.Default); err != nil {
				return fmt.Errorf("record %q default: %w", r.Name, err)
			}
		}
		for _, o := range r.VLANOverrides {
			if !vlanNames[o.VLAN] {
				return fmt.Errorf("record %q references unknown vlan %q", r.Name, o.VLAN)
			}
			if o.Value != nil {
				if err := validateValue(r.Type, *o.Value); err != nil {
					return fmt.Errorf("record %q override for vlan %q: %w", r.Name, o.VLAN, err)
				}
			}
		}
	}
	return nil
}

func validateValue(t model.RecordType, value string) error {
	switch t {
	case model.TypeA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() == nil {
			return fmt.Errorf("%q is not an IPv4 address", value)
		}
	case model.TypeAAAA:
		ip := net.ParseIP(value)
		if ip == nil || ip.To4() != nil {
			return fmt.Errorf("%q is not an IPv6 address", value)
		}
	case model.TypeCNAME:
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("CNAME target is empty")
		}
	}
	return nil
}
