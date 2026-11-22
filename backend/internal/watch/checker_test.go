package watch

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestValidateTargetURL(t *testing.T) {
	valid := map[string]string{
		"https://example.com":               "https://example.com",
		"https://EXAMPLE.com/a?b=1":         "https://example.com/a?b=1",
		"https://example.com:8443/tickets":  "https://example.com:8443/tickets",
		"https://93.184.216.34/status":      "https://93.184.216.34/status",
		"https://[2606:2800:220:1::1]/page": "https://[2606:2800:220:1::1]/page",
	}
	for input, want := range valid {
		got, err := ValidateTargetURL(input)
		if err != nil || got != want {
			t.Errorf("ValidateTargetURL(%q) = %q, %v; want %q", input, got, err, want)
		}
	}

	invalid := []string{
		"",
		"http://example.com",
		"ftp://example.com",
		"https://user:pass@example.com",
		"https://example.com/#fragment",
		"https:///missing-host",
		"https://localhost/",
		"https://app.localhost/",
		"https://127.0.0.1/",
		"https://[::1]/",
		"https://10.0.0.1/",
		"https://169.254.169.254/latest/meta-data",
		"https://100.64.0.1/",
		"https://example.com:0/",
		"https://example.com/a b",
		"https://example.com/" + strings.Repeat("a", 3000),
	}
	for _, input := range invalid {
		if got, err := ValidateTargetURL(input); err == nil {
			t.Errorf("ValidateTargetURL(%q) = %q, want error", input, got)
		}
	}
}

func TestIsPublicIP(t *testing.T) {
	public := []string{
		"8.8.8.8",
		"93.184.216.34",
		"2606:4700:4700::1111",
	}
	for _, raw := range public {
		if !IsPublicIP(netip.MustParseAddr(raw)) {
			t.Errorf("%s should be public", raw)
		}
	}
	private := []string{
		"0.0.0.0",
		"127.0.0.1",
		"127.1.2.3",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.64.0.1",
		"192.0.0.10",
		"198.18.0.1",
		"240.0.0.1",
		"255.255.255.255",
		"::1",
		"::",
		"fc00::1",
		"fe80::1",
		"ff02::1",
		"::ffff:127.0.0.1",
		"2001:db8::1%eth0",
	}
	for _, raw := range private {
		addr, err := netip.ParseAddr(raw)
		if err != nil {
			t.Fatalf("parse test address %q: %v", raw, err)
		}
		if IsPublicIP(addr) {
			t.Errorf("%s should not be public", raw)
		}
	}
}

func TestDialPublicRejectsMixedResolution(t *testing.T) {
	dialed := false
	c := &Checker{
		dial: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("must not dial")
		},
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{
				netip.MustParseAddr("93.184.216.34"),
				netip.MustParseAddr("10.0.0.1"),
			}, nil
		},
	}
	if _, err := c.dialPublic(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("mixed public/private resolution must be refused")
	}
	if dialed {
		t.Fatal("dial must not happen when any resolved address is private")
	}
}

func TestDialPublicRejectsPrivateLiteral(t *testing.T) {
	dialed := false
	c := &Checker{
		dial: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("must not dial")
		},
	}
	if _, err := c.dialPublic(context.Background(), "tcp", "169.254.169.254:443"); err == nil {
		t.Fatal("link-local literal must be refused")
	}
	if dialed {
		t.Fatal("dial must not happen for a private literal")
	}
}

func TestDialPublicPinsResolvedAddress(t *testing.T) {
	var dialedAddress string
	c := &Checker{
		dial: func(_ context.Context, _, address string) (net.Conn, error) {
			dialedAddress = address
			return nil, errors.New("stop after recording")
		},
		lookup: func(context.Context, string, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
		},
	}
	_, _ = c.dialPublic(context.Background(), "tcp", "example.com:443")
	if dialedAddress != "93.184.216.34:443" {
		t.Fatalf("dialed %q, want the resolved IP rather than the hostname", dialedAddress)
	}
}
