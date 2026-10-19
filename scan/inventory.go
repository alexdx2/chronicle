package scan

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/alexdx2/chronicle-core/deps"
	"github.com/alexdx2/chronicle-core/gitutil"
)

// InvFile is one tracked file of the repository.
type InvFile struct {
	Path   string
	Size   int64
	Binary bool
}

// Inventory is the deterministic first look at a repository: what is in it,
// before any model reads anything.
type Inventory struct {
	Files     []InvFile
	ByExt     map[string]int // ".cs" -> n (files without extension under "")
	TopDirs   map[string]int // "src" -> n ("." for root files)
	Manifests []string       // deps.IsManifest
	Infra     []string       // Dockerfile, *.tf, cdk.json, serverless.yml, ...
	Skipped   []string       // binary, over maxBytes, unreadable or not regular: never sent to a model
	// SkipReasons says why a path in Skipped could not be read (stat or read
	// errors); the other skip causes are evident from the file itself.
	SkipReasons map[string]string

	maxBytes int64
	skipped  map[string]bool
	routes   map[string]bool // source files whose text registers HTTP routes
}

// isBinaryHead reports whether the leading bytes of a file mark it binary
// (a NUL within the first 8 KiB).
func isBinaryHead(b []byte) bool {
	if len(b) > 8192 {
		b = b[:8192]
	}
	return bytes.IndexByte(b, 0) >= 0
}

// defaultMaxFileBytes is the size limit used when maxBytes <= 0; it matches
// the cap headless applies to files sent to a model.
const defaultMaxFileBytes int64 = 262144

// BuildInventory lists the files git tracks in root and classifies them.
// A maxBytes <= 0 means defaultMaxFileBytes (256 KiB). Symlinks and other non-regular entries are never read and land in Skipped.
func BuildInventory(root string, maxBytes int64) (*Inventory, error) {
	out, err := gitutil.RunRaw(root, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("inventory: git ls-files: %w", err)
	}
	if maxBytes <= 0 {
		maxBytes = defaultMaxFileBytes
	}
	inv := &Inventory{
		ByExt:    map[string]int{},
		TopDirs:  map[string]int{},
		maxBytes: maxBytes,
		skipped:  map[string]bool{},
		routes:   map[string]bool{},

		SkipReasons: map[string]string{},
	}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" {
			continue
		}
		f := InvFile{Path: p}
		skip := false
		fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(p)))
		switch {
		case err != nil:
			// Tracked but absent from the working copy (deleted): nothing to
			// read. Any other stat error is a file we cannot judge: skipped
			// with its reason, not a failed build.
			skip = true
			if !os.IsNotExist(err) {
				inv.SkipReasons[p] = "stat: " + err.Error()
			}
		case !fi.Mode().IsRegular():
			skip = true
		default:
			f.Size = fi.Size()
			if f.Size > maxBytes {
				skip = true
			} else if head, err := readHead(filepath.Join(root, filepath.FromSlash(p))); err != nil {
				skip = true
				inv.SkipReasons[p] = "read: " + err.Error()
			} else if isBinaryHead(head) {
				f.Binary = true
				skip = true
			}
		}
		inv.Files = append(inv.Files, f)
		inv.ByExt[strings.ToLower(path.Ext(p))]++
		top := "."
		if i := strings.IndexByte(p, '/'); i >= 0 {
			top = p[:i]
		}
		inv.TopDirs[top]++
		if deps.IsManifest(p) {
			inv.Manifests = append(inv.Manifests, p)
		}
		if isInfra(p) {
			inv.Infra = append(inv.Infra, p)
		}
		if skip {
			inv.Skipped = append(inv.Skipped, p)
			inv.skipped[p] = true
		} else if isSourceFile(p) {
			if b, rerr := readHeadN(filepath.Join(root, filepath.FromSlash(p)), maxRouteScanBytes); rerr == nil && routeRegistration.Match(b) {
				inv.routes[p] = true
			}
		}
	}
	return inv, nil
}

func readHeadN(p string, n int64) ([]byte, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return io.ReadAll(io.LimitReader(fh, n))
}

func readHead(p string) ([]byte, error) {
	fh, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	buf := make([]byte, 8192)
	n, err := io.ReadFull(fh, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

func isInfra(p string) bool {
	base := path.Base(p)
	switch base {
	case "Dockerfile", "cdk.json", "serverless.yml", "serverless.yaml",
		"template.yml", "template.yaml", ".gitlab-ci.yml", "Chart.yaml":
		return true
	}
	if strings.HasPrefix(base, "Dockerfile.") || strings.ToLower(path.Ext(p)) == ".tf" {
		return true
	}
	for _, seg := range strings.Split(path.Dir(p), "/") {
		if seg == "helm" {
			return true
		}
	}
	return false
}

// Paths returns the non-skipped tracked files in git order.
func (inv *Inventory) Paths() []string {
	var out []string
	for _, f := range inv.Files {
		if !inv.skipped[f.Path] {
			out = append(out, f.Path)
		}
	}
	return out
}

// Summary renders the prompt section: counts by extension (with up to n sample
// paths each), top directories, manifests, infra and the skipped count.
func (inv *Inventory) Summary(n int) string {
	var b strings.Builder
	samples := map[string][]string{}
	for _, p := range inv.Paths() {
		e := strings.ToLower(path.Ext(p))
		if len(samples[e]) < n {
			samples[e] = append(samples[e], p)
		}
	}
	b.WriteString("extensions:\n")
	for _, k := range sortedByCount(inv.ByExt) {
		fmt.Fprintf(&b, "%s: %d\n", k, inv.ByExt[k])
		for _, p := range samples[k] {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	b.WriteString("top dirs:\n")
	for _, k := range sortedByCount(inv.TopDirs) {
		fmt.Fprintf(&b, "%s: %d\n", k, inv.TopDirs[k])
	}
	b.WriteString("manifests:\n")
	for _, p := range inv.Manifests {
		fmt.Fprintf(&b, "%s\n", p)
	}
	b.WriteString("infra:\n")
	for _, p := range inv.Infra {
		fmt.Fprintf(&b, "%s\n", p)
	}
	fmt.Fprintf(&b, "skipped: %d (binary or over %d bytes)\n", len(inv.Skipped), inv.maxBytes)
	return b.String()
}

// sortedByCount orders keys by count descending, then by name.
func sortedByCount(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

// routeRegistration matches the calls that register an HTTP route in code:
// ASP.NET minimal APIs (MapGet...), Express/Fastify/Koa style (app.get,
// router.post) and the like.
// Express-style calls need a string-literal path starting with "/" as the
// first argument, so `api.get(id)` on an HTTP client is not a route.
var routeRegistration = regexp.MustCompile(`\.Map(Get|Post|Put|Delete|Patch)\s*\(|\b(app|router)\.(get|post|put|delete|patch)\s*\(\s*['"` + "`" + `]/`)

// maxRouteScanBytes bounds how much of a file BuildInventory reads when
// looking for route registrations.
const maxRouteScanBytes = 64 * 1024

// HasRoutes reports whether the file's text registers HTTP routes.
func (inv *Inventory) HasRoutes(p string) bool {
	return inv != nil && inv.routes[p]
}
