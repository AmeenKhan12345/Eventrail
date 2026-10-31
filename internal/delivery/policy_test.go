package delivery

import (
	"net/netip"
	"testing"
)

func TestDefaultPolicy(t *testing.T) {
	tests := []struct {
		addr    string
		blocked bool
	}{
		{addr: "127.0.0.1", blocked: true},
		{addr: "::1", blocked: true},
		{addr: "10.0.0.1", blocked: true},
		{addr: "172.16.0.1", blocked: true},
		{addr: "192.168.1.1", blocked: true},
		{addr: "169.254.169.254", blocked: true},
		{addr: "0.0.0.0", blocked: true},
		{addr: "0.1.2.3", blocked: true},
		{addr: "100.64.0.1", blocked: true},
		{addr: "224.0.0.1", blocked: true},
		{addr: "fc00::1", blocked: true},
		{addr: "fe80::1", blocked: true},
		{addr: "::ffff:10.0.0.1", blocked: true},
		{addr: "8.8.8.8"},
		{addr: "2001:4860:4860::8888"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			err := DefaultPolicy(netip.MustParseAddr(tt.addr))
			if tt.blocked && err == nil {
				t.Fatal("expected the address to be blocked")
			}
			if !tt.blocked && err != nil {
				t.Fatalf("DefaultPolicy: %v", err)
			}
		})
	}
}

func TestPermissivePolicyAllowsLoopback(t *testing.T) {
	if err := PermissivePolicy(netip.MustParseAddr("127.0.0.1")); err != nil {
		t.Fatalf("PermissivePolicy: %v", err)
	}
}
