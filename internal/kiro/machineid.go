package kiro

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// machineID mirrors kiro.rs machine_id.rs so a credential presents the same
// device fingerprint whether kiro-rs or this plugin is using it.
func machineID(c Credential) string {
	if id := normalizeMachineID(c.MachineID); id != "" {
		return id
	}
	if c.AuthMethod == AuthMethodAPIKey {
		return sha256Hex("KiroAPIKey/" + c.KiroAPIKey)
	}
	return sha256Hex("KotlinNativeAPI/" + c.RefreshToken)
}

func normalizeMachineID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) == 64 && isHex(id) {
		return id
	}
	if compact := strings.ReplaceAll(id, "-", ""); len(compact) == 32 && isHex(compact) {
		return compact + compact
	}
	return ""
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
