package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
)

// HashFile reads the file at path and returns its hex-encoded SHA-256 hash.
func HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// HashContent returns the hex-encoded SHA-256 hash of arbitrary content.
func HashContent(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// HashChunkText returns the hex-encoded SHA-256 hash of chunk text.
func HashChunkText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// GenerateChunkID generates a stable chunk ID per FR-006 by hashing
// the composite key "{source}:{path}:{startLine}:{endLine}:{hash}:{model}".
func GenerateChunkID(source, path string, startLine, endLine int, hash, model string) string {
	key := fmt.Sprintf("%s:%s:%d:%d:%s:%s", source, path, startLine, endLine, hash, model)
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// HashSessionFile hashes session file content combined with its lineMap.
// The input to SHA-256 is: content + "\n" + json(lineMap).
func HashSessionFile(content string, lineMap []int) string {
	lineMapJSON, _ := json.Marshal(lineMap)
	input := content + "\n" + string(lineMapJSON)
	sum := sha256.Sum256([]byte(input))
	return hex.EncodeToString(sum[:])
}
