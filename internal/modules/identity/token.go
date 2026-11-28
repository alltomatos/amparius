package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// generateOpaqueToken cria um token opaco de 256 bits (ADR 0048 §6) e o hash
// que é efetivamente persistido. O valor cru nunca é armazenado.
func generateOpaqueToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("gerar token opaco: %w", err)
	}
	raw = hex.EncodeToString(buf)
	return raw, hashOpaqueToken(raw), nil
}

func hashOpaqueToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
