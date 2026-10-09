package updater

import (
	"errors"
	"fmt"
	"strings"

	"aead.dev/minisign"
)

// verifyMinisign checks a minisign signature file over msg using pubKeyB64,
// the base64 public key as printed by `minisign -G`. Both the legacy "Ed"
// algorithm and the pre-hashed "ED" algorithm are accepted; the latter is what
// the aead.dev/minisign CLI (used by the release pipeline) emits for `-S`.
func verifyMinisign(pubKeyB64 string, msg, sigFile []byte) error {
	var pub minisign.PublicKey
	if err := pub.UnmarshalText([]byte(strings.TrimSpace(pubKeyB64))); err != nil {
		return fmt.Errorf("invalid minisign public key: %w", err)
	}
	if !minisign.Verify(pub, msg, sigFile) {
		return errors.New("invalid minisign signature")
	}
	return nil
}
