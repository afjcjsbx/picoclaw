package updater

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// verifyMinisign checks a minisign signature file (legacy "Ed" algorithm, i.e.
// `minisign -S` without -H) over msg using pubKeyB64, the base64 public key
// as printed by `minisign -G`. Both the file signature and the trusted-comment
// global signature must verify.
func verifyMinisign(pubKeyB64 string, msg, sigFile []byte) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64))
	if err != nil || len(pub) != 2+8+ed25519.PublicKeySize || string(pub[:2]) != "Ed" {
		return errors.New("invalid minisign public key")
	}
	keyID, key := pub[2:10], ed25519.PublicKey(pub[10:])

	lines := strings.Split(strings.ReplaceAll(string(sigFile), "\r\n", "\n"), "\n")
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "untrusted comment:") ||
		!strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("malformed minisign signature")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[1]))
	if err != nil || len(sig) != 2+8+ed25519.SignatureSize {
		return errors.New("malformed minisign signature")
	}
	if string(sig[:2]) != "Ed" {
		return fmt.Errorf("unsupported minisign algorithm %q (sign without -H)", sig[:2])
	}
	if !bytes.Equal(sig[2:10], keyID) {
		return errors.New("signature made by a different key")
	}
	if !ed25519.Verify(key, msg, sig[10:]) {
		return errors.New("invalid signature")
	}
	global, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[3]))
	if err != nil || len(global) != ed25519.SignatureSize {
		return errors.New("malformed minisign global signature")
	}
	trusted := strings.TrimPrefix(lines[2], "trusted comment: ")
	if !ed25519.Verify(key, append(append([]byte{}, sig[10:]...), trusted...), global) {
		return errors.New("invalid trusted comment signature")
	}
	return nil
}
