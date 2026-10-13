package headless

import (
	"crypto/sha256"
	"fmt"
	"sort"

	"github.com/alexdx2/chronicle-core/graph/prompts"
	"github.com/alexdx2/chronicle-core/store"
)

// ExtractorVersion is the coverage version of a file extracted with packs:
// "fact-schema@N+packs:<12 hex of sha256 over the sorted "id@version\n" lines>".
// It depends on the applied pack set only, so a changed pack re-sends exactly
// the files that pack applies to.
func ExtractorVersion(packs []prompts.PackRef) string {
	refs := append([]prompts.PackRef(nil), packs...)
	sort.Slice(refs, func(i, j int) bool { return refs[i].ID < refs[j].ID })
	h := sha256.New()
	for _, r := range refs {
		fmt.Fprintf(h, "%s@%s\n", r.ID, r.Version)
	}
	return fmt.Sprintf("fact-schema@%d+packs:%x", store.FactSchemaVersion, h.Sum(nil)[:6])
}
