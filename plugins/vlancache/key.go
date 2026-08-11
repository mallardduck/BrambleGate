package vlancache

import (
	"encoding/binary"
	"hash"
	"hash/fnv"
)

var (
	boolTrue  = []byte{1}
	boolFalse = []byte{0}
)

// hashDirect keys the VLAN-bucketed tier: one entry per (bucket, qname,
// qtype, do, cd). bucket is the requester's matched VLAN name, or
// globalBucket when none matched (or none are declared) — every requester
// then shares the same entry, matching stock cache's ECS-off behavior.
func hashDirect(bucket, qname string, qtype uint16, do, cd bool) uint64 {
	h := fnv.New64()
	writeBool(h, do)
	writeBool(h, cd)
	writeQtype(h, qtype)
	h.Write([]byte(bucket))
	h.Write([]byte{0}) // separator: bounds bucket/qname ambiguity
	h.Write([]byte(qname))
	return h.Sum64()
}

// hashScoped keys the RFC 7871 scope tier: one bucket per (qname, qtype, do,
// cd), holding a small list of (prefix, entry) pairs checked by IP
// containment at lookup time — see store.go.
func hashScoped(qname string, qtype uint16, do, cd bool) uint64 {
	h := fnv.New64()
	writeBool(h, do)
	writeBool(h, cd)
	writeQtype(h, qtype)
	h.Write([]byte(qname))
	return h.Sum64()
}

func writeBool(h hash.Hash, b bool) {
	if b {
		h.Write(boolTrue)
	} else {
		h.Write(boolFalse)
	}
}

func writeQtype(h hash.Hash, qtype uint16) {
	var buf [2]byte
	binary.BigEndian.PutUint16(buf[:], qtype)
	h.Write(buf[:])
}
