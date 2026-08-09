package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	MinArgon2SaltLength  uint32 = 8
	MinArgon2HashLength  uint32 = 16
	MaxArgon2MemoryKiB   uint32 = 64 * 1024
	MaxArgon2Iterations  uint32 = 3
	MaxArgon2Parallelism uint8  = 2
	MaxArgon2SaltLength  uint32 = 64
	MaxArgon2HashLength  uint32 = 64
	MaxArgon2PHCLength          = 512
)

type Argon2idParams struct {
	MemoryKiB   uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	HashLength  uint32
}

func DefaultArgon2idParams() Argon2idParams {
	return Argon2idParams{
		MemoryKiB:   64 * 1024,
		Iterations:  3,
		Parallelism: 2,
		SaltLength:  16,
		HashLength:  32,
	}
}

func HashPassword(password string, params Argon2idParams) (string, error) {
	if err := validateArgon2idParams(params); err != nil {
		return "", err
	}
	salt := make([]byte, params.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, params.HashLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		params.MemoryKiB,
		params.Iterations,
		params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, expected, err := parseArgon2idPHC(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey([]byte(password), salt, params.Iterations, params.MemoryKiB, params.Parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func validateArgon2idParams(params Argon2idParams) error {
	if params.MemoryKiB == 0 || params.MemoryKiB > MaxArgon2MemoryKiB {
		return errors.New("auth: Argon2 memory is outside allowed bounds")
	}
	if params.Iterations == 0 || params.Iterations > MaxArgon2Iterations {
		return errors.New("auth: Argon2 iterations are outside allowed bounds")
	}
	if params.Parallelism == 0 || params.Parallelism > MaxArgon2Parallelism {
		return errors.New("auth: Argon2 parallelism is outside allowed bounds")
	}
	if params.SaltLength < MinArgon2SaltLength || params.SaltLength > MaxArgon2SaltLength {
		return errors.New("auth: Argon2 salt length is outside allowed bounds")
	}
	if params.HashLength < MinArgon2HashLength || params.HashLength > MaxArgon2HashLength {
		return errors.New("auth: Argon2 hash length is outside allowed bounds")
	}
	return nil
}

func parseArgon2idPHC(encoded string) (Argon2idParams, []byte, []byte, error) {
	if len(encoded) == 0 || len(encoded) > MaxArgon2PHCLength {
		return Argon2idParams{}, nil, nil, errors.New("auth: malformed Argon2id hash")
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" {
		return Argon2idParams{}, nil, nil, errors.New("auth: malformed Argon2id hash")
	}
	params, err := parseArgon2idParameterField(parts[3])
	if err != nil {
		return Argon2idParams{}, nil, nil, err
	}
	if len(parts[4]) < base64.RawStdEncoding.EncodedLen(int(MinArgon2SaltLength)) || len(parts[4]) > base64.RawStdEncoding.EncodedLen(int(MaxArgon2SaltLength)) {
		return Argon2idParams{}, nil, nil, errors.New("auth: Argon2 salt is outside allowed bounds")
	}
	if len(parts[5]) < base64.RawStdEncoding.EncodedLen(int(MinArgon2HashLength)) || len(parts[5]) > base64.RawStdEncoding.EncodedLen(int(MaxArgon2HashLength)) {
		return Argon2idParams{}, nil, nil, errors.New("auth: Argon2 hash is outside allowed bounds")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < int(MinArgon2SaltLength) || len(salt) > int(MaxArgon2SaltLength) {
		return Argon2idParams{}, nil, nil, errors.New("auth: malformed Argon2 salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) < int(MinArgon2HashLength) || len(expected) > int(MaxArgon2HashLength) {
		return Argon2idParams{}, nil, nil, errors.New("auth: malformed Argon2 hash")
	}
	params.SaltLength = uint32(len(salt))
	params.HashLength = uint32(len(expected))
	if err := validateArgon2idParams(params); err != nil {
		return Argon2idParams{}, nil, nil, err
	}
	return params, salt, expected, nil
}

func parseArgon2idParameterField(field string) (Argon2idParams, error) {
	fields := strings.Split(field, ",")
	if len(fields) != 3 {
		return Argon2idParams{}, errors.New("auth: malformed Argon2 parameters")
	}
	memory, err := parseBoundedUint(fields[0], "m=", uint64(MaxArgon2MemoryKiB), 32)
	if err != nil {
		return Argon2idParams{}, err
	}
	iterations, err := parseBoundedUint(fields[1], "t=", uint64(MaxArgon2Iterations), 32)
	if err != nil {
		return Argon2idParams{}, err
	}
	parallelism, err := parseBoundedUint(fields[2], "p=", uint64(MaxArgon2Parallelism), 8)
	if err != nil {
		return Argon2idParams{}, err
	}
	params := Argon2idParams{MemoryKiB: uint32(memory), Iterations: uint32(iterations), Parallelism: uint8(parallelism)}
	if params.MemoryKiB == 0 || params.Iterations == 0 || params.Parallelism == 0 {
		return Argon2idParams{}, errors.New("auth: Argon2 parameters must be positive")
	}
	return params, nil
}

func parseBoundedUint(field, prefix string, maximum uint64, bitSize int) (uint64, error) {
	if !strings.HasPrefix(field, prefix) {
		return 0, errors.New("auth: malformed Argon2 parameters")
	}
	valueText := strings.TrimPrefix(field, prefix)
	if valueText == "" || len(valueText) > 10 {
		return 0, errors.New("auth: malformed Argon2 parameters")
	}
	value, err := strconv.ParseUint(valueText, 10, bitSize)
	if err != nil || value > maximum {
		return 0, errors.New("auth: Argon2 parameters are outside allowed bounds")
	}
	return value, nil
}
