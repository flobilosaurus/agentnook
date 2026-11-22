// Package egress mediates guest TCP connections through a host-only approval broker.
package egress

import (
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"net/netip"
)

// flow matches smolvm 1.35.4's SMOLMEG2 prelude. Application bytes are never
// persisted, and guest-provided names are never used as authorization keys.
type flow struct {
	machine string
	target  string
	hint    string
}

func readFlow(r io.Reader, token []byte) (flow, error) {
	var fixed [75]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return flow{}, err
	}
	if string(fixed[:8]) != "SMOLMEG2" || subtle.ConstantTimeCompare(fixed[8:40], token) != 1 {
		return flow{}, errors.New("invalid mediation authentication")
	}
	if [16]byte(fixed[40:56]) == [16]byte{} {
		return flow{}, errors.New("missing machine identity")
	}
	var ip netip.Addr
	switch fixed[72] {
	case 4:
		var addr [4]byte
		if _, err := io.ReadFull(r, addr[:]); err != nil {
			return flow{}, err
		}
		ip = netip.AddrFrom4(addr)
	case 6:
		var addr [16]byte
		if _, err := io.ReadFull(r, addr[:]); err != nil {
			return flow{}, err
		}
		ip = netip.AddrFrom16(addr).Unmap()
	default:
		return flow{}, errors.New("invalid address family")
	}
	var size [2]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return flow{}, err
	}
	n := int(binary.BigEndian.Uint16(size[:]))
	if n > 8192 {
		return flow{}, errors.New("initial payload too large")
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return flow{}, err
	}
	return flow{
		machine: hex.EncodeToString(fixed[40:56]),
		target:  netip.AddrPortFrom(ip, binary.BigEndian.Uint16(fixed[73:75])).String(),
		hint:    serverName(payload),
	}, nil
}

// serverName extracts an optional, UNVERIFIED TLS SNI hint. It does not inspect
// encrypted HTTP, perform DNS lookups, or trust this name for session grants.
func serverName(p []byte) string {
	if len(p) < 9 || p[0] != 22 || p[5] != 1 {
		return ""
	}
	record := int(binary.BigEndian.Uint16(p[3:5]))
	if record+5 > len(p) || record < 4 {
		return ""
	}
	p = p[9 : 5+record]
	take := func(n int) []byte {
		if n < 0 || n > len(p) {
			p = nil
			return nil
		}
		v := p[:n]
		p = p[n:]
		return v
	}
	if len(take(34)) != 34 { // version and random
		return ""
	}
	if len(p) < 1 {
		return ""
	}
	n := int(p[0])
	take(1)
	if len(take(n)) != n || len(p) < 2 {
		return ""
	}
	n = int(binary.BigEndian.Uint16(take(2)))
	if len(take(n)) != n || len(p) < 1 {
		return ""
	}
	n = int(p[0])
	take(1)
	if len(take(n)) != n || len(p) < 2 {
		return ""
	}
	n = int(binary.BigEndian.Uint16(take(2)))
	if len(p) < n {
		return ""
	}
	p = p[:n]
	for len(p) >= 4 {
		kind := binary.BigEndian.Uint16(take(2))
		n = int(binary.BigEndian.Uint16(take(2)))
		ext := take(n)
		if len(ext) != n {
			return ""
		}
		if kind != 0 || len(ext) < 5 {
			continue
		}
		if int(binary.BigEndian.Uint16(ext[:2])) != len(ext)-2 || ext[2] != 0 {
			return ""
		}
		n = int(binary.BigEndian.Uint16(ext[3:5]))
		if n < 1 || n > 253 || len(ext) != 5+n {
			return ""
		}
		name := ext[5:]
		for _, c := range name {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-') {
				return ""
			}
		}
		return string(name)
	}
	return ""
}
