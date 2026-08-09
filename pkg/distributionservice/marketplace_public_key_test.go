package distributionservice

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestNewMarketplacePublicKeyNormalizesNameAndUsesRandomSuffix(t *testing.T) {
	key, err := newMarketplacePublicKey("  Web & Blackbox  ", bytes.NewReader([]byte{0xa3, 0xf9, 0x1c, 0x20}))
	if err != nil {
		t.Fatalf("newMarketplacePublicKey() error = %v", err)
	}
	if got, want := key.String(), "web-blackbox-a3f91c20"; got != want {
		t.Fatalf("key = %q, want %q", got, want)
	}
}

func TestNewMarketplacePublicKeyBoundsName(t *testing.T) {
	key, err := newMarketplacePublicKey(strings.Repeat("a", 200), bytes.NewReader(make([]byte, 4)))
	if err != nil {
		t.Fatalf("newMarketplacePublicKey() error = %v", err)
	}
	if len(key.String()) != MarketplacePublicKeyMaxLength {
		t.Fatalf("key length = %d, want %d", len(key.String()), MarketplacePublicKeyMaxLength)
	}
	if _, err := ParseMarketplacePublicKey(key.String()); err != nil {
		t.Fatalf("generated key is not canonical: %v", err)
	}
}

func TestNewMarketplacePublicKeyRejectsEmptyNormalizedNameAndRandomFailure(t *testing.T) {
	if _, err := newMarketplacePublicKey("团队", bytes.NewReader(make([]byte, 4))); err == nil {
		t.Fatal("non-ASCII-only name was accepted")
	}
	if _, err := newMarketplacePublicKey("web", errorReader{}); err == nil {
		t.Fatal("random source failure was ignored")
	}
}

func TestParseMarketplacePublicKeyRequiresCanonicalForm(t *testing.T) {
	valid := []string{
		"web-blackbox-a3f91c20",
		"marketplace-00000000",
		strings.Repeat("a", MarketplacePublicKeyMaxLength-9) + "-deadbeef",
	}
	for _, raw := range valid {
		key, err := ParseMarketplacePublicKey(raw)
		if err != nil || key.String() != raw {
			t.Fatalf("ParseMarketplacePublicKey(%q) = (%q, %v)", raw, key.String(), err)
		}
	}

	invalid := []string{
		"",
		"550e8400-e29b-41d4-a716-446655440000",
		"Web-blackbox-a3f91c20",
		"web_blackbox-a3f91c20",
		"web-blackbox-A3F91C20",
		"web-blackbox-a3f91c2",
		"web-blackbox-a3f91c200",
		"-web-a3f91c20",
		"web--blackbox-a3f91c20",
		strings.Repeat("a", MarketplacePublicKeyMaxLength-8) + "-deadbeef",
	}
	for _, raw := range invalid {
		if _, err := ParseMarketplacePublicKey(raw); err == nil {
			t.Fatalf("ParseMarketplacePublicKey(%q) succeeded", raw)
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("random unavailable") }
