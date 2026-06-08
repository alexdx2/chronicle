package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PacksDir returns the path to the packs directory (.depbot/packs/).
func (s *Store) PacksDir() string {
	return filepath.Join(s.Dir(), "packs")
}

// SavePackFile saves a pack to .depbot/packs/{slug}.md.
// The slug is derived from the pack ID (e.g., "custom/django" → "django.md").
func (s *Store) SavePackFile(id, content string) error {
	slug := packSlug(id)
	if slug == "" {
		return fmt.Errorf("invalid pack id: %q", id)
	}
	dir := s.PacksDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating packs directory: %w", err)
	}
	path := filepath.Join(dir, slug+".md")
	return os.WriteFile(path, []byte(content), 0644)
}

// LoadPackFile loads a pack from .depbot/packs/{slug}.md.
func (s *Store) LoadPackFile(id string) (string, error) {
	slug := packSlug(id)
	if slug == "" {
		return "", fmt.Errorf("invalid pack id: %q", id)
	}
	path := filepath.Join(s.PacksDir(), slug+".md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ListPackFiles returns the IDs of all packs in .depbot/packs/.
func (s *Store) ListPackFiles() ([]string, error) {
	dir := s.PacksDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".md")
		ids = append(ids, name)
	}
	return ids, nil
}

// packSlug extracts the slug from a pack ID.
// "custom/django" → "django", "nestjs" → "nestjs", "framework/nestjs" → "nestjs"
func packSlug(id string) string {
	if id == "" {
		return ""
	}
	parts := strings.SplitN(id, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return parts[0]
}
