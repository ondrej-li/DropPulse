package domain

import "testing"

func TestNormaliseEventName(t *testing.T) {
	if got, err := NormaliseEventName("  Saturday show  "); err != nil || got != "Saturday show" {
		t.Fatalf("NormaliseEventName = %q, %v", got, err)
	}
	if _, err := NormaliseEventName("   "); err == nil {
		t.Fatal("empty name must be rejected")
	}
	if _, err := NormaliseEventName("bad\nname"); err == nil {
		t.Fatal("control characters must be rejected")
	}
	long := make([]rune, MaxEventNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	if _, err := NormaliseEventName(string(long)); err == nil {
		t.Fatal("over-long name must be rejected")
	}
}

func TestNormaliseCheckInterval(t *testing.T) {
	if got, err := NormaliseCheckInterval(0); err != nil || got != DefaultCheckInterval {
		t.Fatalf("default interval = %d, %v", got, err)
	}
	for _, value := range []int{MinCheckInterval, 900, MaxCheckInterval} {
		if got, err := NormaliseCheckInterval(value); err != nil || got != value {
			t.Fatalf("interval %d = %d, %v", value, got, err)
		}
	}
	for _, value := range []int{-1, 59, 86401} {
		if _, err := NormaliseCheckInterval(value); err == nil {
			t.Fatalf("interval %d must be rejected", value)
		}
	}
}
