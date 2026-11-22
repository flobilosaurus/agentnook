package config

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
)

// NormalizePrivateCIDRs canonicalizes CLI input without interpreting names or
// ambiguous IPv4-mapped IPv6 ranges. No guest-controlled DNS enters this policy.
func NormalizePrivateCIDRs(values []string) ([]string, error) {
	var result []string
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid private-network CIDR %q: use an IPv4 or IPv6 prefix, for example 10.20.0.0/16", value)
		}
		canonical := prefix.Masked().String()
		if !slices.Contains(result, canonical) {
			result = append(result, canonical)
		}
	}
	return result, nil
}

// ValidateNetwork keeps private exceptions from becoming static allow rules
// that bypass smolvm's mediated TCP broker and datagram restrictions.
func (p Project) ValidateNetwork() error {
	if err := ValidateNetwork(p.Network); err != nil {
		return err
	}
	normalized, err := NormalizePrivateCIDRs(p.AllowPrivate)
	if err != nil {
		return err
	}
	if !slices.Equal(normalized, p.AllowPrivate) {
		return errors.New("private-network exceptions must be canonical CIDRs without duplicates")
	}
	if p.Network == "ask" && len(p.AllowPrivate) > 0 {
		return errors.New("private-network exceptions cannot be combined with --network ask: smolvm static allows bypass approvals; use --network full or --clear-allow-private")
	}
	return nil
}
