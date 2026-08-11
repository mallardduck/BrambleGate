package querylog

import (
	"testing"
	"time"
)

func TestEntry_ZeroValueIsUsable(t *testing.T) {
	var e Entry
	if !e.Timestamp.IsZero() {
		t.Errorf("zero Entry.Timestamp = %v, want zero time", e.Timestamp)
	}
	if e.Client != (ClientInfo{}) {
		t.Errorf("zero Entry.Client = %+v, want zero ClientInfo", e.Client)
	}
	if e.QName != "" || e.QType != 0 || e.Verdict != "" || e.Source != "" || e.Rcode != 0 || e.Latency != 0 {
		t.Errorf("zero Entry has unexpected non-zero field: %+v", e)
	}
}

func TestEntry_FieldsRoundTrip(t *testing.T) {
	now := time.Now()
	e := Entry{
		Timestamp: now,
		Client:    ClientInfo{IP: "192.0.2.1", VLAN: "trusted"},
		QName:     "nas.home.arpa.",
		QType:     1, // A
		Verdict:   "local",
		Source:    "localrecords",
		Rcode:     0,
		Latency:   5 * time.Millisecond,
	}

	if e.Timestamp != now {
		t.Errorf("Timestamp = %v, want %v", e.Timestamp, now)
	}
	if e.Client.IP != "192.0.2.1" || e.Client.VLAN != "trusted" {
		t.Errorf("Client = %+v, want IP=192.0.2.1 VLAN=trusted", e.Client)
	}
	if e.QName != "nas.home.arpa." {
		t.Errorf("QName = %q, want %q", e.QName, "nas.home.arpa.")
	}
	if e.Verdict != "local" || e.Source != "localrecords" {
		t.Errorf("Verdict/Source = %q/%q, want local/localrecords", e.Verdict, e.Source)
	}
	if e.Latency != 5*time.Millisecond {
		t.Errorf("Latency = %v, want 5ms", e.Latency)
	}
}
