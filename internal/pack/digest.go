package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"slices"
	"strings"
)

// Digest identifies a package's exact content: "sha256:" followed by hex.
//
// It hashes each file's path and content hash, sorted by path, so it does not
// depend on archive metadata such as timestamps or file order.
type Digest string

// Short returns the first 12 hex characters, for display.
func (d Digest) Short() string {
	s := strings.TrimPrefix(string(d), "sha256:")
	return s[:min(12, len(s))]
}

func digestOf(files map[string][]byte) Digest {
	h := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		sum := sha256.Sum256(files[path])
		h.Write([]byte(path))
		h.Write([]byte{0})
		h.Write([]byte(hex.EncodeToString(sum[:])))
		h.Write([]byte{'\n'})
	}
	return Digest("sha256:" + hex.EncodeToString(h.Sum(nil)))
}
