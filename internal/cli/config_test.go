package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func writeSettings(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	custom := filepath.Join(dir, "custom")
	if err := os.MkdirAll(custom, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(custom, "settings.yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSettingsValid(t *testing.T) {
	dir := writeSettings(t, `
upstream_dns:
  address: 192.168.10.5:53
  protocol: plain
listeners:
  plain: {enabled: true, port: 53}
  dot:   {enabled: true, port: 853}
acme:
  domain: dns.example.com
`)
	s, err := LoadSettings(dir)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if s.UpstreamDNS.Address != "192.168.10.5:53" || !s.Listeners.DoT.Enabled || s.ACME.Domain != "dns.example.com" {
		t.Fatalf("unexpected parse: %+v", s)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]string{
		"no listeners": `
upstream_dns: {address: 1.1.1.1:53}
listeners:
  plain: {enabled: false}
  dot:   {enabled: false}
`,
		"missing upstream": `
listeners:
  plain: {enabled: true, port: 53}
`,
		"bad upstream host:port": `
upstream_dns: {address: not-a-hostport}
listeners:
  plain: {enabled: true, port: 53}
`,
		"dot without domain": `
upstream_dns: {address: 1.1.1.1:53}
listeners:
  dot: {enabled: true, port: 853}
`,
		"bad protocol": `
upstream_dns: {address: 1.1.1.1:53, protocol: carrier-pigeon}
listeners:
  plain: {enabled: true, port: 53}
`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadSettings(writeSettings(t, body)); err == nil {
				t.Fatalf("expected validation error for %q, got nil", name)
			}
		})
	}
}

func TestLoadSettingsMissingFile(t *testing.T) {
	if _, err := LoadSettings(t.TempDir()); err == nil {
		t.Fatal("expected error for missing settings.yaml")
	}
}
