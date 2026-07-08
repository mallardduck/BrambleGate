// Package acme obtains and renews the DoT/DoH certificate via ACME DNS-01, so
// the box never needs to be reachable from the internet — only outbound access
// plus a DNS provider API (docs/certificates.md). It runs as a background manager
// that upgrades/renews the cert in place and triggers the normal engine reload.
package acme

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-acme/lego/v4/challenge"

	"github.com/go-acme/lego/v4/providers/dns/azuredns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/digitalocean"
	"github.com/go-acme/lego/v4/providers/dns/exec"
	"github.com/go-acme/lego/v4/providers/dns/gcloud"
	"github.com/go-acme/lego/v4/providers/dns/hetzner"
	"github.com/go-acme/lego/v4/providers/dns/httpreq"
	"github.com/go-acme/lego/v4/providers/dns/linode"
	"github.com/go-acme/lego/v4/providers/dns/namecheap"
	"github.com/go-acme/lego/v4/providers/dns/ovh"
	"github.com/go-acme/lego/v4/providers/dns/rfc2136"
	"github.com/go-acme/lego/v4/providers/dns/route53"
)

// Provider describes one supported DNS-01 provider: how to construct it (reads
// its own credentials from the environment) and which env vars the user needs to
// set. The env-var list is a hint for docs/GUI — lego is the source of truth.
type Provider struct {
	Name    string   // canonical config value for acme.dns_provider
	Display string   // human-friendly label
	EnvVars []string // primary credential env vars the user must set
	Docs    string   // lego docs URL for the full list
	new     func() (challenge.Provider, error)
}

const legoDoc = "https://go-acme.github.io/lego/dns/"

// registry is the curated set of first-class providers. Anything outside it is
// still reachable via the "exec" / "httpreq" universal providers below. Adding a
// provider is one import + one entry here.
var registry = map[string]Provider{
	"cloudflare": {
		Name: "cloudflare", Display: "Cloudflare",
		EnvVars: []string{"CLOUDFLARE_DNS_API_TOKEN"},
		Docs:    legoDoc + "cloudflare/",
		new:     func() (challenge.Provider, error) { return cloudflare.NewDNSProvider() },
	},
	"route53": {
		Name: "route53", Display: "AWS Route 53",
		EnvVars: []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_REGION"},
		Docs:    legoDoc + "route53/",
		new:     func() (challenge.Provider, error) { return route53.NewDNSProvider() },
	},
	"gcloud": {
		Name: "gcloud", Display: "Google Cloud DNS",
		EnvVars: []string{"GCE_PROJECT", "GCE_SERVICE_ACCOUNT"},
		Docs:    legoDoc + "gcloud/",
		new:     func() (challenge.Provider, error) { return gcloud.NewDNSProvider() },
	},
	"digitalocean": {
		Name: "digitalocean", Display: "DigitalOcean",
		EnvVars: []string{"DO_AUTH_TOKEN"},
		Docs:    legoDoc + "digitalocean/",
		new:     func() (challenge.Provider, error) { return digitalocean.NewDNSProvider() },
	},
	"azuredns": {
		Name: "azuredns", Display: "Azure DNS",
		EnvVars: []string{"AZURE_CLIENT_ID", "AZURE_CLIENT_SECRET", "AZURE_TENANT_ID", "AZURE_SUBSCRIPTION_ID", "AZURE_RESOURCE_GROUP"},
		Docs:    legoDoc + "azuredns/",
		new:     func() (challenge.Provider, error) { return azuredns.NewDNSProvider() },
	},
	"ovh": {
		Name: "ovh", Display: "OVH",
		EnvVars: []string{"OVH_ENDPOINT", "OVH_APPLICATION_KEY", "OVH_APPLICATION_SECRET", "OVH_CONSUMER_KEY"},
		Docs:    legoDoc + "ovh/",
		new:     func() (challenge.Provider, error) { return ovh.NewDNSProvider() },
	},
	"rfc2136": {
		Name: "rfc2136", Display: "RFC2136 dynamic update (nsupdate)",
		EnvVars: []string{"RFC2136_NAMESERVER", "RFC2136_TSIG_KEY", "RFC2136_TSIG_SECRET", "RFC2136_TSIG_ALGORITHM"},
		Docs:    legoDoc + "rfc2136/",
		new:     func() (challenge.Provider, error) { return rfc2136.NewDNSProvider() },
	},
	"namecheap": {
		Name: "namecheap", Display: "Namecheap",
		EnvVars: []string{"NAMECHEAP_API_USER", "NAMECHEAP_API_KEY"},
		Docs:    legoDoc + "namecheap/",
		new:     func() (challenge.Provider, error) { return namecheap.NewDNSProvider() },
	},
	"linode": {
		Name: "linode", Display: "Linode (Akamai)",
		EnvVars: []string{"LINODE_TOKEN"},
		Docs:    legoDoc + "linode/",
		new:     func() (challenge.Provider, error) { return linode.NewDNSProvider() },
	},
	"hetzner": {
		Name: "hetzner", Display: "Hetzner",
		EnvVars: []string{"HETZNER_API_KEY"},
		Docs:    legoDoc + "hetzner/",
		new:     func() (challenge.Provider, error) { return hetzner.NewDNSProvider() },
	},
	// Universal escape hatches — cover any provider not first-classed above,
	// without importing that provider's SDK.
	"exec": {
		Name: "exec", Display: "exec (custom script)",
		EnvVars: []string{"EXEC_PATH"},
		Docs:    legoDoc + "exec/",
		new:     func() (challenge.Provider, error) { return exec.NewDNSProvider() },
	},
	"httpreq": {
		Name: "httpreq", Display: "httpreq (webhook)",
		EnvVars: []string{"HTTPREQ_ENDPOINT"},
		Docs:    legoDoc + "httpreq/",
		new:     func() (challenge.Provider, error) { return httpreq.NewDNSProvider() },
	},
}

// aliases maps friendlier config spellings to canonical registry keys.
var aliases = map[string]string{
	"azure":       "azuredns",
	"dnsupdate":   "rfc2136", // lego's rfc2136 provider also reads DNSUPDATE_* env vars
	"google":      "gcloud",
	"googlecloud": "gcloud",
	"do":          "digitalocean",
	"aws":         "route53",
}

func canonical(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if c, ok := aliases[n]; ok {
		return c
	}
	return n
}

// LookupProvider returns the registered provider metadata for a config name.
func LookupProvider(name string) (Provider, bool) {
	p, ok := registry[canonical(name)]
	return p, ok
}

// newChallengeProvider constructs the lego DNS-01 solver for a provider name.
func newChallengeProvider(name string) (challenge.Provider, error) {
	p, ok := registry[canonical(name)]
	if !ok {
		return nil, fmt.Errorf("unsupported dns_provider %q (supported: %s; or use exec/httpreq)", name, strings.Join(SupportedProviders(), ", "))
	}
	return p.new()
}

// SupportedProviders lists the canonical provider names, sorted.
func SupportedProviders() []string {
	out := make([]string, 0, len(registry))
	for k := range registry {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
