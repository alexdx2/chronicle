package gitutil

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// objectFormatCache remembers each repoRoot's git object-hash format so
// BlobSHA — called once per file during a scan — does not shell out to `git
// rev-parse` on every single call.
var (
	objectFormatMu    sync.Mutex
	objectFormatCache = map[string]string{}
)

// objectFormat returns the git object hash format ("sha1" or "sha256") for
// the repository at repoRoot, per `git rev-parse --show-object-format`,
// cached per repoRoot. Outside a repository — or when git cannot answer — it
// defaults to "sha1", git's universal format before object-format existed.
func objectFormat(repoRoot string) string {
	objectFormatMu.Lock()
	defer objectFormatMu.Unlock()
	if f, ok := objectFormatCache[repoRoot]; ok {
		return f
	}
	format := "sha1"
	if out, err := Run(repoRoot, "rev-parse", "--show-object-format"); err == nil {
		if strings.TrimSpace(out) == "sha256" {
			format = "sha256"
		}
	}
	objectFormatCache[repoRoot] = format
	return format
}

// BlobSHA returns the git blob object id that `git hash-object` would compute
// for content in the repository at repoRoot. It hashes locally rather than
// shelling out to `git hash-object` per file — the dominant cost in a scan
// touching thousands of files — using the header git itself hashes a blob
// with: "blob <content-length>\0<content>". The digest algorithm (sha1 or
// sha256) follows the repository's own object format (see objectFormat).
func BlobSHA(repoRoot string, content []byte) (string, error) {
	header := fmt.Sprintf("blob %d\x00", len(content))
	var sum []byte
	switch objectFormat(repoRoot) {
	case "sha256":
		h := sha256.New()
		h.Write([]byte(header))
		h.Write(content)
		sum = h.Sum(nil)
	default:
		h := sha1.New()
		h.Write([]byte(header))
		h.Write(content)
		sum = h.Sum(nil)
	}
	return hex.EncodeToString(sum), nil
}

// FileBlobSHA returns the git blob id of repoRoot/relPath's current on-disk
// content (see BlobSHA).
func FileBlobSHA(repoRoot, relPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(repoRoot, relPath))
	if err != nil {
		return "", fmt.Errorf("FileBlobSHA: %w", err)
	}
	return BlobSHA(repoRoot, data)
}
