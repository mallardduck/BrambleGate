package model

import (
	"strings"
	"testing"
)

func TestParseDomainName(t *testing.T) {
	long63 := strings.Repeat("a", 63)
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string // substring of the error; empty means success
	}{
		{name: "plain", in: "host.example.com", want: "host.example.com."},
		{name: "already fqdn", in: "host.example.com.", want: "host.example.com."},
		{name: "mixed case and padding", in: "  Host.Example.COM  ", want: "host.example.com."},
		{name: "single label", in: "localhost", want: "localhost."},
		{name: "underscore service label", in: "_dmarc.example.com", want: "_dmarc.example.com."},
		{name: "ptr name", in: "1.0.168.192.in-addr.arpa", want: "1.0.168.192.in-addr.arpa."},
		{name: "max label length", in: long63 + ".example.com", want: long63 + ".example.com."},

		{name: "empty", in: "", wantErr: "empty"},
		{name: "whitespace only", in: "   ", wantErr: "empty"},
		{name: "root", in: ".", wantErr: "empty"},
		{name: "url with scheme", in: "https://foo.com/", wantErr: "scheme"},
		{name: "path", in: "foo.com/path", wantErr: "path"},
		{name: "port", in: "foo.com:53", wantErr: "port"},
		{name: "ipv4 literal", in: "192.168.1.10", wantErr: "IP address"},
		{name: "ipv6 literal", in: "fe80::1", wantErr: "IP address"},
		{name: "inner whitespace", in: "foo bar.com", wantErr: "whitespace"},
		{name: "empty label", in: "foo..com", wantErr: "empty label"},
		{name: "leading dot", in: ".foo.com", wantErr: "empty label"},
		{name: "label too long", in: strings.Repeat("a", 64) + ".com", wantErr: "63"},
		{name: "name too long", in: strings.Repeat(long63+".", 4) + "com", wantErr: "253"},
		{name: "wildcard", in: "*.example.com", wantErr: "wildcard"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseDomainName(tc.in)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ParseDomainName(%q) = %q, want error containing %q", tc.in, got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ParseDomainName(%q) error = %q, want it to contain %q", tc.in, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseDomainName(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseDomainName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
