package httputil

import (
	"regexp"
	"strings"
)

// CID validation regex - basic IPFS CID pattern (v0 and v1)
// v0: Qm... (base58, 46 characters)
// v1: b... or z... (base32/base58, variable length)
var cidRegex = regexp.MustCompile(`^(Qm[1-9A-HJ-NP-Za-km-z]{44}|b[a-z2-7]{58,}|z[1-9A-HJ-NP-Za-km-z]{48,})$`)

// ValidateCID checks if a string is a valid IPFS CID.
func ValidateCID(cid string) bool {
	return cidRegex.MatchString(strings.TrimSpace(cid))
}

// ValidateNamespace checks if a namespace name is valid.
// Valid namespaces must:
// - Not be empty after trimming
// - Only contain alphanumeric characters, hyphens, and underscores
// - Start with a letter or number
// - Be between 1 and 64 characters
var namespaceRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func ValidateNamespace(ns string) bool {
	ns = strings.TrimSpace(ns)
	if ns == "" {
		return false
	}
	return namespaceRegex.MatchString(ns)
}

// ValidateTopicName checks if a pubsub topic name is valid.
// Valid topics must:
// - Not be empty after trimming
// - Only contain alphanumeric characters, hyphens, underscores, slashes, and dots
// - Be between 1 and 256 characters
var topicRegex = regexp.MustCompile(`^[a-zA-Z0-9._/-]{1,256}$`)

func ValidateTopicName(topic string) bool {
	topic = strings.TrimSpace(topic)
	if topic == "" {
		return false
	}
	return topicRegex.MatchString(topic)
}

// ValidateWalletAddress checks if a string looks like an Ethereum wallet address.
// Valid addresses are 40 hex characters, optionally prefixed with "0x".
var walletRegex = regexp.MustCompile(`^(0x)?[0-9a-fA-F]{40}$`)

func ValidateWalletAddress(wallet string) bool {
	return walletRegex.MatchString(strings.TrimSpace(wallet))
}

// NormalizeWalletAddress normalizes a wallet address by removing "0x" prefix and converting to lowercase.
func NormalizeWalletAddress(wallet string) string {
	wallet = strings.TrimSpace(wallet)
	wallet = strings.TrimPrefix(wallet, "0x")
	wallet = strings.TrimPrefix(wallet, "0X")
	return strings.ToLower(wallet)
}

// IsEmpty checks if a string is empty after trimming whitespace.
func IsEmpty(s string) bool {
	return strings.TrimSpace(s) == ""
}

// IsNotEmpty checks if a string is not empty after trimming whitespace.
func IsNotEmpty(s string) bool {
	return strings.TrimSpace(s) != ""
}

// ValidateDMapName checks if a distributed map name is valid.
// Valid dmap names must:
// - Not be empty after trimming
// - Only contain alphanumeric characters, hyphens, underscores, and dots
// - Be between 1 and 128 characters
var dmapRegex = regexp.MustCompile(`^[a-zA-Z0-9._-]{1,128}$`)

func ValidateDMapName(dmap string) bool {
	dmap = strings.TrimSpace(dmap)
	if dmap == "" {
		return false
	}
	return dmapRegex.MatchString(dmap)
}
