// Package lan hosts one temporary account-free transfer on a specific local address.
package lan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

// Address describes one private interface suitable for a shareable LAN origin.
type Address struct{ Interface, IP string }

// Addresses enumerates usable private addresses without consulting external services.
// IPv6 link-local addresses require device-specific zone identifiers and are not
// portable share links, so they are deliberately excluded.
func Addresses() ([]Address, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Address
	seen := map[string]bool{}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 || containerInterface(iface.Name) {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			return nil, err
		}
		for _, raw := range addresses {
			prefix, err := netip.ParsePrefix(raw.String())
			if err != nil {
				continue
			}
			ip := prefix.Addr().Unmap()
			if !ip.IsPrivate() || seen[ip.String()] {
				continue
			}
			seen[ip.String()] = true
			out = append(out, Address{iface.Name, ip.String()})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Interface != out[j].Interface {
			return out[i].Interface < out[j].Interface
		}
		return out[i].IP < out[j].IP
	})
	return out, nil
}

// Container-only bridges are usually unreachable from another LAN device.
// Explicit --listen still permits their private addresses for container use.
// Keep ordinary bridge names (such as br0) available for bridged home networks.
func containerInterface(name string) bool {
	if name == "docker0" || name == "podman0" || name == "cni0" || strings.HasPrefix(name, "veth") {
		return true
	}
	if !strings.HasPrefix(name, "br-") || len(name) != 15 {
		return false
	}
	for _, c := range name[3:] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// ValidateListen accepts a specific private or loopback numeric address and a
// port including zero. Public, unspecified, multicast and zoned addresses fail.
func ValidateListen(address string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("use --listen PRIVATE_IP:0 (or [PRIVATE_IPV6]:0); port 0 selects an available port")
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || ip.Zone() != "" || !(ip.IsPrivate() || ip.IsLoopback()) {
		return errors.New("--listen requires a specific private or loopback IP; public and wildcard addresses are not supported")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 0 || n > 65535 {
		return errors.New("--listen port must be between 0 and 65535")
	}
	return nil
}

// Listen binds once and retains the listener, avoiding free-port probing races.
func Listen(ctx context.Context, address string) (net.Listener, error) {
	if err := ValidateListen(address); err != nil {
		return nil, err
	}
	listener, err := new(net.ListenConfig).Listen(ctx, "tcp", address)
	if err != nil {
		return nil, fmt.Errorf("cannot bind selected local address; check that it belongs to this computer and use port 0 if the requested port is occupied: %w", err)
	}
	return listener, nil
}
