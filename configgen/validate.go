package configgen

import (
	"errors"
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
		return errors.New("acme.domain is required when an encrypted listener (DoT/DoH/DoQ) is enabled")
	}
	if err := validateACME(s.ACME); err != nil {
		return err
	}
	if err := validateMDNS(s.MDNS, s.VLANs); err != nil {
		return err
	}
	return validateRecords(rs, s.VLANs)
}

// validateMDNS checks the mDNS block: suffixes must stay inside the owned zone,
// and selector VLAN/family references must be valid.
func validateMDNS(m model.MDNS, vlans []model.VLAN) error {
	if !m.Enabled {
		return nil
	}
	vlanNames := map[string]bool{}
	for _, v := range vlans {
		vlanNames[v.Name] = true
	}
	if err := validateSuffix(m.Suffix); err != nil {
		return fmt.Errorf("mdns.suffix: %w", err)
	}
	for i, s := range m.AutoPublish {
		if err := validateSelector(s, vlanNames, fmt.Sprintf("mdns.auto_publish[%d]", i)); err != nil {
			return err
		}
	}
	for i, r := range m.Naming {
		if err := validateSelector(r.Match, vlanNames, fmt.Sprintf("mdns.naming[%d].match", i)); err != nil {
			return err
		}
		if err := validateSuffix(r.Suffix); err != nil {
			return fmt.Errorf("mdns.naming[%d].suffix: %w", i, err)
		}
	}
	return nil
}

// validateSuffix requires a naming suffix to be the owned zone or a subdomain of
// it, so discovered names never fall outside where localrecords is authoritative.
func validateSuffix(suffix string) error {
	if suffix == "" {
		return nil // defaults to OwnedZone
	}
	s := strings.TrimSuffix(strings.ToLower(suffix), ".")
	if s != OwnedZone && !strings.HasSuffix(s, "."+OwnedZone) {
		return fmt.Errorf("%q must be %q or a subdomain of it", suffix, OwnedZone)
	}
	return nil
}

// validateACME checks the fields ACME issuance needs when it is turned on. The
// dns_provider name's validity against the supported set is checked by the acme
// package (which owns the registry); here we just require the fields be present.
func validateACME(a model.ACME) error {
	if !a.Enabled {
		return nil
	}
	if strings.TrimSpace(a.Domain) == "" {
		return errors.New("acme.domain is required when acme.enabled is true")
	}
	if strings.TrimSpace(a.Email) == "" {
		return errors.New("acme.email is required when acme.enabled is true")
	}
	if strings.TrimSpace(a.DNSProvider) == "" {
		return errors.New("acme.dns_provider is required when acme.enabled is true")
	}
	if a.RenewBeforeDays < 0 {
		return errors.New("acme.renew_before_days must not be negative")
	}
	return nil
}

func validateListeners(l model.Listeners) error {
	enabled := false
	for name, ln := range map[string]model.Listener{
		"plain": l.Plain, "dot": l.DoT, "doh": l.DoH, "doq": l.DoQ,
	} {
		if !ln.Enabled {
			continue
		}
		enabled = true
		if ln.Port <= 0 || ln.Port > 65535 {
			return fmt.Errorf("listeners.%s.port %d is out of range 1-65535", name, ln.Port)
		}
	}
	if !enabled {
		return errors.New("no listeners enabled: enable at least one of plain/dot/doh/doq")
	}
	return nil
}

func validateUpstream(u model.UpstreamTarget) error {
	if strings.TrimSpace(u.Address) == "" {
		return errors.New("upstream_dns.address is required")
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

// ownedCIDR tracks a parsed CIDR back to the VLAN it belongs to, for overlap
// error messages across every CIDR of every VLAN.
type ownedCIDR struct {
	vlan string
	cidr string
	net  *net.IPNet
}

func validateVLANs(vlans []model.VLAN) error {
	var nets []ownedCIDR
	seen := map[string]bool{}
	for _, v := range vlans {
		if strings.TrimSpace(v.Name) == "" {
			return errors.New("a vlan is missing a name")
		}
		if seen[v.Name] {
			return fmt.Errorf("duplicate vlan name %q", v.Name)
		}
		seen[v.Name] = true

		if len(v.CIDRs) == 0 {
			return fmt.Errorf("vlan %q must list at least one cidr", v.Name)
		}
		for _, c := range v.CIDRs {
			_, ipnet, err := net.ParseCIDR(c)
			if err != nil {
				return fmt.Errorf("vlan %q cidr %q is invalid: %w", v.Name, c, err)
			}
			for _, other := range nets {
				if netsOverlap(ipnet, other.net) {
					return fmt.Errorf("vlan %q cidr %q overlaps vlan %q cidr %q", v.Name, c, other.vlan, other.cidr)
				}
			}
			nets = append(nets, ownedCIDR{vlan: v.Name, cidr: c, net: ipnet})
		}
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
			return errors.New("a record is missing a name")
		}
		if !strings.HasSuffix(name, "."+OwnedZone+".") && name != OwnedZone+"." {
			return fmt.Errorf("record %q is outside the owned zone %q", r.Name, OwnedZone)
		}

		key := name + "/" + string(r.Type)
		if seen[key] {
			return fmt.Errorf("duplicate record for %q type %s", r.Name, r.Type)
		}
		seen[key] = true

		if r.IsMDNS() {
			if err := validateMDNSRecord(r, vlanNames); err != nil {
				return err
			}
			continue
		}

		switch r.Type {
		case model.TypeA, model.TypeAAAA, model.TypeCNAME:
		default:
			return fmt.Errorf("record %q has unsupported type %q", r.Name, r.Type)
		}
		if r.Match != nil {
			return fmt.Errorf("record %q: match is only valid for type mdns", r.Name)
		}

		if r.Default != "" {
			if err := validateValue(r.Type, r.Default); err != nil {
				return fmt.Errorf("record %q default: %w", r.Name, err)
			}
		}

		if err := validateOverrides(r, vlanNames); err != nil {
			return err
		}

		// A record must be able to answer for at least one VLAN: either it has a
		// default, or some override supplies a value.
		if r.Default == "" && !anyOverrideProvidesValue(r) {
			return fmt.Errorf("record %q has no default and no vlan_override supplies a value", r.Name)
		}
	}
	return nil
}

// validateMDNSRecord checks a live mDNS-linked record: it must carry a Match
// selector and nothing static.
func validateMDNSRecord(r model.Record, vlanNames map[string]bool) error {
	if r.Match == nil || r.Match.IsZero() {
		return fmt.Errorf("mdns record %q must set a non-empty match selector", r.Name)
	}
	if r.Default != "" || r.TTL != 0 || len(r.VLANOverrides) != 0 {
		return fmt.Errorf("mdns record %q must not set default/ttl/vlan_overrides (value comes live from mDNS)", r.Name)
	}
	return validateSelector(*r.Match, vlanNames, fmt.Sprintf("mdns record %q", r.Name))
}

// validateSelector checks a selector's VLAN reference and family value.
func validateSelector(s model.Selector, vlanNames map[string]bool, ctx string) error {
	if s.VLAN != "" && !vlanNames[s.VLAN] {
		return fmt.Errorf("%s references unknown vlan %q", ctx, s.VLAN)
	}
	switch strings.ToLower(s.Family) {
	case "", "ipv4", "ipv6":
	default:
		return fmt.Errorf("%s has invalid family %q (want ipv4 or ipv6)", ctx, s.Family)
	}
	return nil
}

func validateOverrides(r model.Record, vlanNames map[string]bool) error {
	seen := map[string]bool{}
	for _, o := range r.VLANOverrides {
		if !vlanNames[o.VLAN] {
			return fmt.Errorf("record %q references unknown vlan %q", r.Name, o.VLAN)
		}
		if seen[o.VLAN] {
			return fmt.Errorf("record %q has duplicate override for vlan %q", r.Name, o.VLAN)
		}
		seen[o.VLAN] = true

		if o.NXDomain && (o.Value != "" || o.TTL != 0) {
			return fmt.Errorf("record %q override for vlan %q: nxdomain cannot be combined with value/ttl", r.Name, o.VLAN)
		}
		// A non-nxdomain override that supplies neither a value nor a ttl is a
		// no-op that likely masks a mistake.
		if !o.NXDomain && o.Value == "" && o.TTL == 0 {
			return fmt.Errorf("record %q override for vlan %q does nothing (set value, ttl, or nxdomain)", r.Name, o.VLAN)
		}
		if !o.NXDomain && o.Value != "" {
			if err := validateValue(r.Type, o.Value); err != nil {
				return fmt.Errorf("record %q override for vlan %q: %w", r.Name, o.VLAN, err)
			}
		}
		// A ttl-only override (empty value) requires the record to have a default
		// to inherit; otherwise there is nothing to answer with.
		if !o.NXDomain && o.Value == "" && r.Default == "" {
			return fmt.Errorf("record %q override for vlan %q is ttl-only but the record has no default value to inherit", r.Name, o.VLAN)
		}
	}
	return nil
}

func anyOverrideProvidesValue(r model.Record) bool {
	for _, o := range r.VLANOverrides {
		if !o.NXDomain && o.Value != "" {
			return true
		}
	}
	return false
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
			return errors.New("CNAME target is empty")
		}
	case model.TypeMDNS:
		// mdns records carry no static value (it resolves live from the discovery
		// table); they never reach here — validateRecords handles them separately.
	}
	return nil
}
