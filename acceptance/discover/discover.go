// Package discover fetches live server state from a running BrambleGate
// instance's read-only /api/* endpoints, so acceptance.yaml doesn't need to
// hand-duplicate config that's already there (vlan names, upstream address,
// hosts.yaml entries, mdns-linked records). Deliberately decodes into local,
// minimal structs rather than importing the model package — the acceptance
// module talks to BrambleGate only over the network, never via its Go
// packages (see ../README.md's "Why its own module").
package discover

import (
	"context"
	"fmt"

	"github.com/mallardduck/BrambleGate/acceptance/apiclient"
)

// VLAN mirrors the fields of model.Settings.VLANs this suite needs: just
// enough to cross-check a declared acceptance.yaml VLAN name against what
// the server actually has configured.
type VLAN struct {
	Name string `json:"name"`
}

// Host mirrors GET /api/hosts's model.Host shape.
type Host struct {
	IP       string   `json:"ip"`
	Hostname string   `json:"hostname"`
	Aliases  []string `json:"aliases,omitempty"`
}

// Names returns Hostname followed by every Aliases entry, mirroring
// model.Host.Names().
func (h Host) Names() []string {
	return append([]string{h.Hostname}, h.Aliases...)
}

// MDNSRecord is one records.yaml entry of type "mdns" — a live record whose
// value is resolved from the mDNS discovery table at query time.
type MDNSRecord struct {
	Name string
}

// State is the server state discovered once at startup and threaded through
// config.Config, so per-VLAN/per-host/per-record checks don't each re-fetch it.
type State struct {
	VLANs           []VLAN
	UpstreamAddress string
	Hosts           []Host
	MDNSRecords     []MDNSRecord
}

// Fetch discovers live state from apiBase's /api/settings, /api/hosts, and
// /api/records endpoints.
func Fetch(ctx context.Context, apiBase string) (*State, error) {
	var settings struct {
		VLANs       []VLAN `json:"vlans"`
		UpstreamDNS struct {
			Address string `json:"address"`
		} `json:"upstream_dns"`
	}
	if err := apiclient.GetJSON(ctx, apiBase, "/api/settings", &settings); err != nil {
		return nil, fmt.Errorf("discover /api/settings: %w", err)
	}

	var hosts struct {
		Hosts []Host `json:"hosts"`
	}
	if err := apiclient.GetJSON(ctx, apiBase, "/api/hosts", &hosts); err != nil {
		return nil, fmt.Errorf("discover /api/hosts: %w", err)
	}

	var records struct {
		Records []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"records"`
	}
	if err := apiclient.GetJSON(ctx, apiBase, "/api/records", &records); err != nil {
		return nil, fmt.Errorf("discover /api/records: %w", err)
	}
	var mdnsRecords []MDNSRecord
	for _, r := range records.Records {
		if r.Type == "mdns" {
			mdnsRecords = append(mdnsRecords, MDNSRecord{Name: r.Name})
		}
	}

	return &State{
		VLANs:           settings.VLANs,
		UpstreamAddress: settings.UpstreamDNS.Address,
		Hosts:           hosts.Hosts,
		MDNSRecords:     mdnsRecords,
	}, nil
}
