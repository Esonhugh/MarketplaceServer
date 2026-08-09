package distributionservice

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"regexp"
	"strings"
)

const (
	MarketplacePublicKeyMaxLength  = 128
	marketplacePublicKeySuffixSize = 4
	marketplacePublicKeySuffixLen  = marketplacePublicKeySuffixSize * 2
	marketplacePublicKeyNameMaxLen = MarketplacePublicKeyMaxLength - marketplacePublicKeySuffixLen - 1
)

var marketplacePublicKeyPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*-[0-9a-f]{8}$`)

type MarketplacePublicKey struct {
	value string
}

func ParseMarketplacePublicKey(raw string) (MarketplacePublicKey, error) {
	if len(raw) == 0 || len(raw) > MarketplacePublicKeyMaxLength || !marketplacePublicKeyPattern.MatchString(raw) {
		return MarketplacePublicKey{}, errors.New("marketplace public key must be canonical lowercase kebab-case with an eight-character hexadecimal suffix")
	}
	return MarketplacePublicKey{value: raw}, nil
}

func NewMarketplacePublicKey(name string) (MarketplacePublicKey, error) {
	return newMarketplacePublicKey(name, rand.Reader)
}

func (key MarketplacePublicKey) String() string {
	return key.value
}

func newMarketplacePublicKey(name string, randomness io.Reader) (MarketplacePublicKey, error) {
	normalized := normalizeMarketplaceName(name)
	if normalized == "" {
		return MarketplacePublicKey{}, errors.New("marketplace name must contain an ASCII letter or digit")
	}
	if len(normalized) > marketplacePublicKeyNameMaxLen {
		normalized = strings.Trim(normalized[:marketplacePublicKeyNameMaxLen], "-")
	}
	var suffix [marketplacePublicKeySuffixSize]byte
	if _, err := io.ReadFull(randomness, suffix[:]); err != nil {
		return MarketplacePublicKey{}, errors.New("generate marketplace public key randomness: " + err.Error())
	}
	return ParseMarketplacePublicKey(normalized + "-" + hex.EncodeToString(suffix[:]))
}

func normalizeMarketplaceName(name string) string {
	var normalized strings.Builder
	normalized.Grow(min(len(name), marketplacePublicKeyNameMaxLen))
	separator := false
	for _, character := range name {
		if normalized.Len() >= marketplacePublicKeyNameMaxLen {
			break
		}
		switch {
		case character >= 'A' && character <= 'Z':
			if separator && normalized.Len() > 0 {
				normalized.WriteByte('-')
			}
			normalized.WriteByte(byte(character + ('a' - 'A')))
			separator = false
		case character >= 'a' && character <= 'z' || character >= '0' && character <= '9':
			if separator && normalized.Len() > 0 {
				normalized.WriteByte('-')
			}
			normalized.WriteRune(character)
			separator = false
		default:
			separator = normalized.Len() > 0
		}
	}
	return normalized.String()
}
