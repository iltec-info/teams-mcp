package secrets

import (
	"errors"
	"strings"
	"testing"
)

// Talks to the real OS secret store using a throwaway entry name.
func TestRoundTripChunkedAndShrink(t *testing.T) {
	const name = "test-roundtrip-entry"
	t.Cleanup(func() { _ = Delete(name) })

	big := strings.Repeat(`{"k":"значение-é-✓"}`, 900) // ~18k chars, multi-byte, many chunks
	if err := Set(name, big); err != nil {
		t.Skipf("secret store unavailable: %v", err)
	}
	got, err := Get(name)
	if err != nil || got != big {
		t.Fatalf("big roundtrip failed: err=%v equal=%v", err, got == big)
	}

	if err := Set(name, "small"); err != nil {
		t.Fatal(err)
	}
	if got, _ := Get(name); got != "small" {
		t.Fatalf("shrink: got %q", got)
	}

	if err := Delete(name); err != nil {
		t.Fatal(err)
	}
	if _, err := Get(name); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete want ErrNotFound, got %v", err)
	}
}
