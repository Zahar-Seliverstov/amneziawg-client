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
		return "", fmt.Errorf("ключ не разобран из base64: %w", err)
	}

	// WireGuard keys are 32 bytes
	if len(keyBytes) != 32 {
		return "", fmt.Errorf("неверная длина ключа: ожидалось 32 байта, получено %d", len(keyBytes))
	}

	// Encode to hex
	return hex.EncodeToString(keyBytes), nil
}
