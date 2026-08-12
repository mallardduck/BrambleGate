package mdnsadvertise

import (
	"strings"

	"github.com/mallardduck/BrambleGate/model"
	"github.com/mallardduck/BrambleGate/plugins/mdnsadvertise/mdnsresponder"
)

// instanceNamePrefix is the base of every service's mDNS-SD instance name.
// The responder backend keys registrations by InstanceName alone (not
// InstanceName+ServiceType), so a process registering more than one service
// type — as this one does — needs a distinct instance name per type; see
// instanceNameFor.
const instanceNamePrefix = "BrambleGate"

// instanceNameFor derives a per-service-type instance name, e.g.
// "_domain._udp.local" -> "BrambleGate-domain-udp". Used both when building the
// desired service set and when reconstructing a service ID to unregister.
func instanceNameFor(serviceType string) string {
	label := strings.TrimSuffix(serviceType, ".local")
	label = strings.TrimPrefix(label, "_")
	label = strings.ReplaceAll(label, "._", "-")
	return instanceNamePrefix + "-" + label
}

// desiredServices computes the mDNS-SD service set this process should be
// advertising for the given settings. Do53 (plain DNS) uses the IANA-registered
// service name for port 53, "domain" (CoreDNS's plain server block serves both
// UDP and TCP, so both service types are advertised). Encrypted transports
// follow draft-liu-add-dnssd-edns-01; DoH/DoQ aren't rendered by configgen yet,
// so they're not advertised until a later phase implements those listeners.
// DesiredServices is the exported form of desiredServices, for callers (the
// settings UI) that want to show what would be/is being advertised without
// needing a live Advertiser — the service set is a pure function of settings.
func DesiredServices(settings model.Settings) []*mdnsresponder.ServiceSpec {
	return desiredServices(settings)
}

func desiredServices(settings model.Settings) []*mdnsresponder.ServiceSpec {
	var out []*mdnsresponder.ServiceSpec

	if settings.Listeners.Plain.Enabled {
		port := uint16(settings.Listeners.Plain.Port)
		for _, serviceType := range []string{"_domain._udp.local", "_domain._tcp.local"} {
			out = append(out, &mdnsresponder.ServiceSpec{Name: instanceNameFor(serviceType), Type: serviceType, Port: port})
		}
	}

	if settings.Listeners.DoT.Enabled {
		const serviceType = "_dot._tcp.local"
		txt := map[string]string{"alpn": "dot"}
		if settings.ACME.Domain != "" {
			txt["domain"] = settings.ACME.Domain
		}
		out = append(out, &mdnsresponder.ServiceSpec{
			Name: instanceNameFor(serviceType),
			Type: serviceType,
			Port: uint16(settings.Listeners.DoT.Port),
			TXT:  txt,
		})
	}

	return out
}
