package auth_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestAPIKeyGenerationAndParsing(t *testing.T) {
	t.Parallel()

	first, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	second, err := auth.GenerateAPIKey()
	if err != nil {
		t.Fatalf("GenerateAPIKey: %v", err)
	}
	if first == second {
		t.Fatal("two generated API keys were identical")
	}
	if !strings.HasPrefix(first, auth.APIKeyPrefix) {
		t.Fatalf("API key %q does not have prefix %q", first, auth.APIKeyPrefix)
	}
	if len(first) != len(auth.APIKeyPrefix)+auth.APIKeyEncodedLength {
		t.Fatalf("API key length = %d", len(first))
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(first, auth.APIKeyPrefix))
	if err != nil {
		t.Fatalf("API key is not unpadded base64url: %v", err)
	}
	if len(decoded) != auth.APIKeyRandomBytes {
		t.Fatalf("random payload length = %d", len(decoded))
	}
	if err := auth.ValidateAPIKey(first); err != nil {
		t.Fatalf("ValidateAPIKey: %v", err)
	}
}

func TestAPIKeyPrefixIdentifiesMalformedKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"mpsk_",
		"mpsk_short",
		"mpsk_" + strings.Repeat("A", auth.APIKeyEncodedLength+1),
		"mpsk_" + strings.Repeat("+", auth.APIKeyEncodedLength),
		"mpsk_" + strings.Repeat("A", auth.APIKeyEncodedLength-1) + "=",
	} {
		if !auth.HasAPIKeyPrefix(key) {
			t.Fatalf("prefixed malformed key %q was not identified", key)
		}
		if err := auth.ValidateAPIKey(key); !errors.Is(err, auth.ErrMalformedAPIKey) {
			t.Fatalf("ValidateAPIKey(%q) error = %v", key, err)
		}
	}
	if auth.HasAPIKeyPrefix("ordinary password") {
		t.Fatal("ordinary password was classified as an API key")
	}
}

func TestAPIKeyIndexUsesCompletePlaintextKey(t *testing.T) {
	t.Parallel()

	key := "mpsk_" + base64.RawURLEncoding.EncodeToString(make([]byte, auth.APIKeyRandomBytes))
	pepper := []byte("0123456789abcdef0123456789abcdef")

	got, err := auth.IndexAPIKey(key, pepper)
	if err != nil {
		t.Fatalf("IndexAPIKey: %v", err)
	}
	mac := hmac.New(sha256.New, pepper)
	_, _ = mac.Write([]byte(key))
	want := auth.APIKeyIndexPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if got != want {
		t.Fatalf("index = %q, want %q", got, want)
	}

	again, err := auth.IndexAPIKey(key, append([]byte(nil), pepper...))
	if err != nil || again != got {
		t.Fatalf("index is not deterministic: %q, %v", again, err)
	}
	otherKey := "mpsk_" + base64.RawURLEncoding.EncodeToString(append(make([]byte, auth.APIKeyRandomBytes-1), 1))
	other, err := auth.IndexAPIKey(otherKey, pepper)
	if err != nil {
		t.Fatalf("IndexAPIKey(other): %v", err)
	}
	if other == got {
		t.Fatal("different complete keys produced the same index")
	}
}

func TestAPIKeyIndexRejectsWeakPepperAndMalformedKeys(t *testing.T) {
	t.Parallel()

	key := "mpsk_" + base64.RawURLEncoding.EncodeToString(make([]byte, auth.APIKeyRandomBytes))
	if _, err := auth.IndexAPIKey(key, make([]byte, auth.MinimumAPIKeyPepperBytes-1)); !errors.Is(err, auth.ErrWeakAPIKeyPepper) {
		t.Fatalf("short pepper error = %v", err)
	}
	if _, err := auth.IndexAPIKey("mpsk_bad", make([]byte, auth.MinimumAPIKeyPepperBytes)); !errors.Is(err, auth.ErrMalformedAPIKey) {
		t.Fatalf("malformed key error = %v", err)
	}
}
