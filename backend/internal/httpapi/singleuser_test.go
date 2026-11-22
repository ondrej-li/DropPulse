package httpapi

import "testing"

func TestIsLoopbackHost(t *testing.T) {
	for _, host := range []string{"localhost:8090", "LOCALHOST:8090", "127.0.0.1:8090", "[::1]:8090", "localhost"} {
		if !isLoopbackHost(host) {
			t.Errorf("%q should be a loopback host", host)
		}
	}
	for _, host := range []string{"evil.example:8090", "10.0.0.1:8090", "localhost.evil.example:8090", "0.0.0.0:8090", ""} {
		if isLoopbackHost(host) {
			t.Errorf("%q must not be accepted as loopback", host)
		}
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin string
		host   string
		want   bool
	}{
		{"http://localhost:8090", "localhost:8090", true},
		{"http://127.0.0.1:8090", "127.0.0.1:8090", true},
		{"http://[::1]:8090", "[::1]:8090", true},
		{"http://localhost", "localhost:80", true},
		{"http://localhost:8090", "127.0.0.1:8090", false},
		{"https://localhost:8090", "localhost:8090", false},
		{"http://localhost:8090", "localhost:9090", false},
		{"http://localhost:8090/path", "localhost:8090", false},
		{"http://user@localhost:8090", "localhost:8090", false},
		{"null", "localhost:8090", false},
		{"", "localhost:8090", false},
	}
	for _, test := range cases {
		if got := sameOrigin(test.origin, test.host); got != test.want {
			t.Errorf("sameOrigin(%q, %q) = %v, want %v", test.origin, test.host, got, test.want)
		}
	}
}
