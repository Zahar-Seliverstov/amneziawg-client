package vpn

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// KeyToHex converts a base64 WireGuard key to hex format for UAPI
func KeyToHex(base64Key string) (string, error) {
	// Decode from base64
	keyBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return "", fmt.Errorf("invalid base64 key: %w", err)
	}

	// WireGuard keys are 32 bytes
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("invalid key length: expected 32 bytes, got %d", len(keyBytes))
	}

	// Encode to hex
	return hex.EncodeToString(keyBytes), nil
}
