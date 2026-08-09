// Package corefile is a tiny builder for CoreDNS Corefile server blocks —
// replaces manual strings.Builder/Fprintf brace-and-tab bookkeeping in
// configgen with a flat, ordered list of directive calls. Directive order is
// insertion order (a plain []string under append, not a map — Go slices
// never randomize iteration order), which matters here since it determines
// where each plugin sits relative to the fixed dnsserver.Directives chain's
// intent (docs/dns-engine.md).
package corefile

import (
	"fmt"
	"strings"
)

// Block is one Corefile server block (or, via SubBlock, a nested plugin
// block like quic{}/localrecords{}). Zero value is not usable for a
// top-level block — use NewBlock; SubBlock constructs nested blocks itself.
type Block struct {
	addr  string
	lines []string
}

// NewBlock starts a top-level server block for the given bind address
// (e.g. ".:53", "tls://.:853").
func NewBlock(addr string) *Block {
	return &Block{addr: addr}
}

// Directive appends one line, formatted like fmt.Sprintf. Args are optional —
// a directive with no arguments (e.g. "cache") is just the literal format
// string.
func (b *Block) Directive(format string, args ...any) *Block {
	b.lines = append(b.lines, fmt.Sprintf(format, args...))
	return b
}

// DirectiveIf appends the directive only when cond is true — the common case
// of an optional plugin line gated on a settings field.
func (b *Block) DirectiveIf(cond bool, format string, args ...any) *Block {
	if cond {
		b.Directive(format, args...)
	}
	return b
}

// SubBlock appends a nested "header {...}" block, e.g. quic{} or
// localrecords{}. inner is called with a fresh Block to populate the nested
// lines, which are then indented one level deeper than b's own lines.
func (b *Block) SubBlock(header string, inner func(*Block)) *Block {
	nested := &Block{}
	inner(nested)
	b.lines = append(b.lines, header+" {")
	for _, l := range nested.lines {
		b.lines = append(b.lines, "\t"+l)
	}
	b.lines = append(b.lines, "}")
	return b
}

// String renders the block: "addr {\n" + one tab-indented line per directive
// (nested SubBlock lines already carry their own extra indent) + "}\n".
func (b *Block) String() string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s {\n", b.addr)
	for _, l := range b.lines {
		fmt.Fprintf(&sb, "\t%s\n", l)
	}
	sb.WriteString("}\n")
	return sb.String()
}
