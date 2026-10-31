package delivery

import (
	"errors"
	"fmt"
	"net/netip"
)

// errDestinationBlocked marks an address the sender must not connect to.
var errDestinationBlocked = errors.New("blocked destination")

// IPPolicy reports whether a resolved address may be connected to.
type IPPolicy func(netip.Addr) error

var (
	cgnatPrefix       = netip.MustParsePrefix("100.64.0.0/10")
	thisNetworkPrefix = netip.MustParsePrefix("0.0.0.0/8")
	uniqueLocalPrefix = netip.MustParsePrefix("fc00::/7")
)

// DefaultPolicy rejects addresses that must not receive webhooks:
// loopback, private, link-local, unspecified, multicast, CGNAT, 0.0.0.0/8,
// and IPv6 unique-local. IPv4-mapped IPv6 addresses are unmapped first.
func DefaultPolicy(addr netip.Addr) error {
	if !addr.IsValid() {
		return fmt.Errorf("%w: invalid address", errDestinationBlocked)
	}
	addr = addr.Unmap()
	switch {
	case addr.IsLoopback():
		return blocked(addr, "loopback")
	case addr.IsPrivate():
		return blocked(addr, "private")
	case addr.IsLinkLocalUnicast():
		return blocked(addr, "link-local")
	case addr.IsLinkLocalMulticast():
		return blocked(addr, "link-local multicast")
	case addr.IsUnspecified():
		return blocked(addr, "unspecified")
	case addr.IsMulticast():
		return blocked(addr, "multicast")
	case cgnatPrefix.Contains(addr):
		return blocked(addr, "cgnat")
	case thisNetworkPrefix.Contains(addr):
		return blocked(addr, "this network")
	case uniqueLocalPrefix.Contains(addr):
		return blocked(addr, "unique local")
	default:
		return nil
	}
}

// PermissivePolicy allows every address. Tests use it to reach httptest servers.
func PermissivePolicy(netip.Addr) error {
	return nil
}

func blocked(addr netip.Addr, reason string) error {
	return fmt.Errorf("%w: %s is %s", errDestinationBlocked, addr, reason)
}
