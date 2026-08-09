package auth_test

import (
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

func TestArgon2idPasswordRoundTrip(t *testing.T) {
	t.Parallel()

	params := auth.Argon2idParams{
		MemoryKiB:   64,
		Iterations:  1,
		Parallelism: 1,
		SaltLength:  8,
		HashLength:  16,
	}
	encoded, err := auth.HashPassword("correct horse battery staple", params)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("unexpected PHC string: %q", encoded)
	}

	valid, err := auth.VerifyPassword("correct horse battery staple", encoded)
	if err != nil || !valid {
		t.Fatalf("VerifyPassword valid = %v, %v", valid, err)
	}
	valid, err = auth.VerifyPassword("wrong password", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword mismatch: %v", err)
	}
	if valid {
		t.Fatal("wrong password verified")
	}
}

func TestArgon2idProductionDefaults(t *testing.T) {
	t.Parallel()

	got := auth.DefaultArgon2idParams()
	want := auth.Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		HashLength:  32,
	}
	if got != want {
		t.Fatalf("defaults = %+v, want %+v", got, want)
	}
}

func TestArgon2idRejectsInvalidHashParameters(t *testing.T) {
	t.Parallel()

	valid := auth.Argon2idParams{MemoryKiB: 64, Iterations: 1, Parallelism: 1, SaltLength: 8, HashLength: 16}
	for _, tc := range []struct {
		name   string
		mutate func(*auth.Argon2idParams)
	}{
		{name: "zero memory", mutate: func(p *auth.Argon2idParams) { p.MemoryKiB = 0 }},
		{name: "zero iterations", mutate: func(p *auth.Argon2idParams) { p.Iterations = 0 }},
		{name: "zero parallelism", mutate: func(p *auth.Argon2idParams) { p.Parallelism = 0 }},
		{name: "zero salt", mutate: func(p *auth.Argon2idParams) { p.SaltLength = 0 }},
		{name: "zero hash", mutate: func(p *auth.Argon2idParams) { p.HashLength = 0 }},
		{name: "memory too large", mutate: func(p *auth.Argon2idParams) { p.MemoryKiB = auth.MaxArgon2MemoryKiB + 1 }},
		{name: "iterations too large", mutate: func(p *auth.Argon2idParams) { p.Iterations = auth.MaxArgon2Iterations + 1 }},
		{name: "parallelism too large", mutate: func(p *auth.Argon2idParams) { p.Parallelism = auth.MaxArgon2Parallelism + 1 }},
		{name: "salt too large", mutate: func(p *auth.Argon2idParams) { p.SaltLength = auth.MaxArgon2SaltLength + 1 }},
		{name: "hash too large", mutate: func(p *auth.Argon2idParams) { p.HashLength = auth.MaxArgon2HashLength + 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := valid
			tc.mutate(&params)
			if _, err := auth.HashPassword("password", params); err == nil {
				t.Fatal("HashPassword unexpectedly succeeded")
			}
		})
	}
}

func TestArgon2idRejectsMalformedAndOversizedPHC(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"argon2id$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=16$m=64,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=x,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1,extra=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$t=1,m=64,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=00000000064,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$***$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$***",
		"$argon2id$v=19$m=64,t=1,p=1$$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$",
		"$argon2id$v=19$m=1048577,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=11,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=33$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$" + strings.Repeat("A", 1000) + "$aGFzaA",
		"$argon2id$v=19$m=64,t=1,p=1$c2FsdA$" + strings.Repeat("A", 1000),
		strings.Repeat("x", auth.MaxArgon2PHCLength+1),
	}
	for i, encoded := range cases {
		if valid, err := auth.VerifyPassword("password", encoded); err == nil || valid {
			t.Fatalf("case %d: VerifyPassword = %v, %v", i, valid, err)
		}
	}
}
