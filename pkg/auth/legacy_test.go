package auth_test

import (
	"errors"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestLegacyJWTSkeletonFailsClosed(t *testing.T) {
	t.Parallel()

	if _, err := auth.GenToken(auth.Info{UID: "user-1"}); !errors.Is(err, auth.ErrLegacyJWTDisabled) {
		t.Fatalf("GenToken error = %v", err)
	}
	if _, err := auth.ParseToken("any-token"); !errors.Is(err, auth.ErrLegacyJWTDisabled) {
		t.Fatalf("ParseToken error = %v", err)
	}
}
