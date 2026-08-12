package querylog

import "testing"

func TestSetHostNames_IsHostName_NormalizesCaseAndFQDN(t *testing.T) {
	defer SetHostNames(nil)
	SetHostNames([]string{"NAS.home.arpa", "printer.home.arpa."})

	for _, qname := range []string{"nas.home.arpa.", "NAS.HOME.ARPA.", "nas.home.arpa", "printer.home.arpa."} {
		if !isHostName(qname) {
			t.Errorf("isHostName(%q) = false, want true", qname)
		}
	}
	if isHostName("unknown.home.arpa.") {
		t.Error("isHostName(unknown.home.arpa.) = true, want false")
	}
}

func TestIsHostName_NoSetYet_ReturnsFalse(t *testing.T) {
	defer SetHostNames(nil)
	SetHostNames(nil)
	if isHostName("nas.home.arpa.") {
		t.Error("isHostName with an empty set = true, want false")
	}
}
