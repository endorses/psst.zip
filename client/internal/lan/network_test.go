package lan

import (
	"context"
	"net"
	"testing"
)

func TestAddressChooserExcludesContainerBridgesOnly(t *testing.T) {
	for _, name := range []string{"docker0", "br-02bc475b35f6", "podman0", "cni0", "veth123"} {
		if !containerInterface(name) {
			t.Errorf("container-only interface %s would clutter LAN choices", name)
		}
	}
	for _, name := range []string{"enp58s0", "wlan0", "br0", "br-lan", "bridge0"} {
		if containerInterface(name) {
			t.Errorf("usable LAN interface %s hidden", name)
		}
	}
}

func TestListenPrivatePortAndIsolation(t *testing.T) {
	for _, value := range []string{"0.0.0.0:0", "[::]:0", "8.8.8.8:0", "example.com:0", "127.0.0.1:-1", "127.0.0.1:65536", "[fe80::1%eth0]:0"} {
		if err := ValidateListen(value); err == nil {
			t.Errorf("accepted unsafe bind %q", value)
		}
	}
	for _, value := range []string{"127.0.0.1:0", "192.168.1.2:8080", "[fd00::1]:0", "[::1]:0"} {
		if err := ValidateListen(value); err != nil {
			t.Errorf("rejected %q: %v", value, err)
		}
	}
	// Occupying the conventional port cannot interfere with default :0 sessions.
	conventional, err := net.Listen("tcp", "127.0.0.1:8080")
	if err == nil {
		defer conventional.Close()
	}
	first, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Listen(context.Background(), "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if first.Addr().String() == second.Addr().String() {
		t.Fatal("simultaneous sessions share a port")
	}
	if _, err = Listen(context.Background(), first.Addr().String()); err == nil {
		t.Fatal("explicit occupied port silently changed")
	}
}
