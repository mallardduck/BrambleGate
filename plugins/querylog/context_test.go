package querylog

import (
	"context"
	"testing"
)

func TestFromContext_NoEntryStashed_ReturnsNil(t *testing.T) {
	if got := FromContext(context.Background()); got != nil {
		t.Errorf("FromContext(no entry) = %v, want nil", got)
	}
}

func TestNewContext_RoundTrip(t *testing.T) {
	e := &Entry{QName: "nas.home.arpa."}
	ctx := NewContext(context.Background(), e)

	got := FromContext(ctx)
	if got != e {
		t.Fatalf("FromContext returned %p, want the same pointer %p", got, e)
	}

	// The accessor hands back the same pointer specifically so a downstream
	// plugin's in-place mutation (Source/Verdict) is visible to querylog's own
	// handler after next.ServeDNS returns.
	got.Source = "localrecords"
	if e.Source != "localrecords" {
		t.Errorf("mutation through FromContext's pointer did not propagate: e.Source = %q", e.Source)
	}
}

func TestNewContext_NilEntry_FromContextReturnsNil(t *testing.T) {
	ctx := NewContext(context.Background(), nil)
	if got := FromContext(ctx); got != nil {
		t.Errorf("FromContext(stashed nil) = %v, want nil", got)
	}
}
