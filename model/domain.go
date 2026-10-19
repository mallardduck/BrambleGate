package model

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode"
)

// Wire-format limits from RFC 1035 §2.3.4: a label is at most 63 octets and a
// name at most 253 characters in presentation form (255 on the wire, which
// also counts the length octets and the root label).
const (
	maxLabelLen  = 63
	maxDomainLen = 253
)

// ParseDomainName validates a user-typed domain name and returns it in the
// canonical form the DNS engine keys on: lower-cased, fully qualified
// (trailing dot).
//
// It enforces wire-format validity, not hostname (LDH) rules, so underscore
// service labels (_dmarc.example.com) and reverse-lookup names
// (1.0.168.192.in-addr.arpa) are accepted — both are legitimate DNS names a
// resolver can hold. The failure modes it names specifically (scheme, path,
// port, IP literal, wildcard) are the ones a pasted URL or address produces, so
// the error can tell the user what to fix instead of silently matching nothing.
//
// Stdlib-only on purpose: model has no dependencies (see doc.go).
func ParseDomainName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "." {
		return "", errors.New("domain name is empty")
	}
	if strings.Contains(s, "://") {
		return "", errors.New("enter a bare domain name, not a URL (remove the scheme, e.g. https://)")
	}
	if strings.Contains(s, "/") {
		return "", errors.New("enter a bare domain name, not a URL (remove the path after the slash)")
	}
	if net.ParseIP(s) != nil {
		return "", errors.New("enter a domain name, not an IP address")
	}
	if strings.Contains(s, ":") {
		return "", errors.New("enter a bare domain name without a port")
	}
	if strings.ContainsFunc(s, unicode.IsSpace) {
		return "", errors.New("domain name must not contain whitespace")
	}
	if strings.Contains(s, "*") {
		return "", errors.New("wildcards are not supported; enter an exact name")
	}
	for _, r := range s {
		if r < 0x21 || r > 0x7e {
			return "", fmt.Errorf("domain name contains an invalid character %q (use the punycode form for non-ASCII names)", r)
		}
	}

	name := strings.ToLower(strings.TrimSuffix(s, "."))
	if len(name) > maxDomainLen {
		return "", fmt.Errorf("domain name is longer than %d characters", maxDomainLen)
	}
	for label := range strings.SplitSeq(name, ".") {
		if label == "" {
			return "", errors.New("domain name has an empty label (stray or doubled dot)")
		}
		if len(label) > maxLabelLen {
			return "", fmt.Errorf("label %q is longer than %d characters", label, maxLabelLen)
		}
	}
	return name + ".", nil
}
