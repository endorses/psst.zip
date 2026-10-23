package store

import (
	"errors"
	"strings"
)

// Payloads always have exactly two opaque components: transfer/file. Restrict
// names before any filesystem call; never clean a hostile key into a valid one.
func payloadParts(key string) (string, string, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 2 {
		return "", "", errors.New("invalid payload key")
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 255 || part == "." || part == ".." {
			return "", "", errors.New("invalid payload key")
		}
		for _, c := range part {
			if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
				return "", "", errors.New("invalid payload key")
			}
		}
	}
	return parts[0], parts[1], nil
}
