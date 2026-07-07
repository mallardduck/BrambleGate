package cli

import (
	"strings"
	"testing"
)

func TestRenderCorefileBothListeners(t *testing.T) {
	s := Settings{
		UpstreamDNS: UpstreamDNS{Address: "192.168.10.5:53", Protocol: "plain"},
		Listeners: Listeners{
			Plain: Listener{Enabled: true, Port: 53},
			DoT:   Listener{Enabled: true, Port: 853},
		},
		ACME: ACME{Domain: "dns.example.com"},
	}
	got := string(renderCorefile(s, "/c/cert.pem", "/c/key.pem"))

	for _, want := range []string{
		".:53 {",
		"tls://.:853 {",
		"tls /c/cert.pem /c/key.pem",
		"forward . 192.168.10.5:53",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered Corefile missing %q:\n%s", want, got)
		}
	}
	if strings.Count(got, "forward .") != 2 {
		t.Errorf("expected forward in both blocks:\n%s", got)
	}
}

func TestRenderCorefilePlainOnlyHasNoTLS(t *testing.T) {
	s := Settings{
		UpstreamDNS: UpstreamDNS{Address: "10.0.0.1:53"},
		Listeners:   Listeners{Plain: Listener{Enabled: true, Port: 5353}},
	}
	got := string(renderCorefile(s, "", ""))
	if strings.Contains(got, "tls") {
		t.Errorf("plain-only Corefile should have no tls directive:\n%s", got)
	}
	if !strings.Contains(got, ".:5353 {") {
		t.Errorf("expected plain block on :5353:\n%s", got)
	}
}

func TestForwardTargetDoTWrapsInTLS(t *testing.T) {
	s := Settings{UpstreamDNS: UpstreamDNS{Address: "9.9.9.9:853", Protocol: "dot"}}
	if got := s.forwardTarget(); got != "tls://9.9.9.9:853" {
		t.Errorf("forwardTarget dot = %q, want tls://9.9.9.9:853", got)
	}
}
