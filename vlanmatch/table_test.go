package vlanmatch

import (
	"net"
	"testing"
)

func TestLookupPrecedence(t *testing.T) {
	tbl := NewTable([]VLAN{
		{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}},
		{Name: "guest", CIDRs: []string{"192.168.20.0/24", "not-a-cidr"}},
	})

	cases := []struct {
		ip       string
		wantName string
		wantOK   bool
	}{
		{"192.168.10.5", "trusted", true},
		{"192.168.20.5", "guest", true},
		{"10.0.0.1", "", false},
	}
	for _, c := range cases {
		name, ok := tbl.Lookup(net.ParseIP(c.ip))
		if name != c.wantName || ok != c.wantOK {
			t.Errorf("Lookup(%s) = (%q, %v), want (%q, %v)", c.ip, name, ok, c.wantName, c.wantOK)
		}
	}
}

func TestLookupNilIP(t *testing.T) {
	tbl := NewTable([]VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}})
	if _, ok := tbl.Lookup(nil); ok {
		t.Error("Lookup(nil) should never match")
	}
}

func TestZeroTableMatchesNothing(t *testing.T) {
	var tbl Table
	if _, ok := tbl.Lookup(net.ParseIP("192.168.10.5")); ok {
		t.Error("zero Table should match nothing")
	}
}

func TestCurrentDefaultsToZeroTable(t *testing.T) {
	SetCurrent(Table{})
	if _, ok := Current().Lookup(net.ParseIP("192.168.10.5")); ok {
		t.Error("Current() before any real SetCurrent call should match nothing")
	}
}

func TestSetCurrentRoundtrip(t *testing.T) {
	t.Cleanup(func() { SetCurrent(Table{}) })

	SetCurrent(NewTable([]VLAN{{Name: "trusted", CIDRs: []string{"192.168.10.0/24"}}}))
	name, ok := Current().Lookup(net.ParseIP("192.168.10.5"))
	if !ok || name != "trusted" {
		t.Errorf("Current().Lookup after SetCurrent = (%q, %v), want (\"trusted\", true)", name, ok)
	}
}
