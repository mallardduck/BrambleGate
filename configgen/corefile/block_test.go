package corefile

import "testing"

func TestBlockRendersDirectivesInCallOrder(t *testing.T) {
	b := NewBlock(".:53")
	b.Directive("tls %s %s", "/c/cert.pem", "/c/key.pem")
	b.Directive("forward . %s", "192.168.10.5:53")
	b.Directive("cache")

	want := ".:53 {\n" +
		"\ttls /c/cert.pem /c/key.pem\n" +
		"\tforward . 192.168.10.5:53\n" +
		"\tcache\n" +
		"}\n"
	if got := b.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBlockDirectiveIfOmitsWhenFalse(t *testing.T) {
	b := NewBlock(".:53")
	b.DirectiveIf(false, "mdnsbridge")
	b.DirectiveIf(true, "cache")

	want := ".:53 {\n\tcache\n}\n"
	if got := b.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBlockSubBlockNestsWithDoubleIndent(t *testing.T) {
	b := NewBlock(".:8853")
	b.SubBlock("quic", func(inner *Block) {
		inner.Directive("max_streams %d", 256)
		inner.Directive("worker_pool_size %d", 512)
	})
	b.Directive("cache")

	want := ".:8853 {\n" +
		"\tquic {\n" +
		"\t\tmax_streams 256\n" +
		"\t\tworker_pool_size 512\n" +
		"\t}\n" +
		"\tcache\n" +
		"}\n"
	if got := b.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestBlockSubBlockCanNestEmpty(t *testing.T) {
	b := NewBlock(".:53")
	b.SubBlock("localrecords home.arpa", func(inner *Block) {
		inner.Directive("zonedata /config/.runtime/zones/records.json")
	})

	want := ".:53 {\n" +
		"\tlocalrecords home.arpa {\n" +
		"\t\tzonedata /config/.runtime/zones/records.json\n" +
		"\t}\n" +
		"}\n"
	if got := b.String(); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}
