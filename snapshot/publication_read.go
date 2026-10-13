// depbot/snapshot/publication_read.go
package snapshot

import (
	"fmt"
	"sort"
	"strings"
)

// TypesPath is where the optional types registry lives in a tree.
const TypesPath = ".depbot/chronicle.types.yaml"

// ProfilePath is where the optional repo profile (format version 2) lives.
const ProfilePath = ".depbot/profile.json"

// ReadPublicationFiles reads the snapshot files out of a publication commit.
// graph.jsonl and the domain manifest are required, the types and profile files are not.
func ReadPublicationFiles(repoRoot, commit string) (Files, error) {
	var f Files
	if !ValidCodeSha(commit) {
		return f, ErrBadSha
	}
	names, err := runGit(repoRoot, "ls-tree", "-r", "--name-only", "--full-tree", "--end-of-options", commit)
	if err != nil {
		return f, fmt.Errorf("snapshot: ReadPublicationFiles: %w", err)
	}
	have := map[string]bool{}
	for _, n := range strings.Split(strings.TrimSpace(names), "\n") {
		have[n] = true
	}
	if !have[Path] || !have[ManifestPath] {
		return f, fmt.Errorf("snapshot: publication %s lacks %s or %s", commit, Path, ManifestPath)
	}
	read := func(p string) ([]byte, error) {
		b, err := runGitBytes(repoRoot, "cat-file", "blob", "--end-of-options", commit+":"+p)
		if err != nil {
			return nil, fmt.Errorf("snapshot: ReadPublicationFiles: read %s at %s: %w", p, commit, err)
		}
		return b, nil
	}
	if f.Graph, err = read(Path); err != nil {
		return Files{}, err
	}
	if f.Domain, err = read(ManifestPath); err != nil {
		return Files{}, err
	}
	if have[TypesPath] {
		if f.Types, err = read(TypesPath); err != nil {
			return Files{}, err
		}
	}
	if have[ProfilePath] {
		if f.Profile, err = read(ProfilePath); err != nil {
			return Files{}, err
		}
	}
	return f, nil
}

// HeadRefs lists every chronicle/heads/* ref, remote-tracking refs first. A
// local head whose sha already has a remote twin is dropped (the remote is
// the published truth), so each sha appears once.
func HeadRefs(repoRoot string) ([]string, error) {
	out, err := runGit(repoRoot, "for-each-ref", "--format=%(refname)", HeadRefPrefix, RemoteHeadRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("snapshot: HeadRefs: %w", err)
	}
	remote := map[string]bool{}
	var remotes, locals []string
	for _, r := range strings.Split(strings.TrimSpace(out), "\n") {
		switch {
		case r == "":
		case strings.HasPrefix(r, RemoteHeadRefPrefix):
			remote[strings.TrimPrefix(r, RemoteHeadRefPrefix)] = true
			remotes = append(remotes, r)
		case strings.HasPrefix(r, HeadRefPrefix):
			locals = append(locals, r)
		}
	}
	sort.Strings(remotes)
	sort.Strings(locals)
	refs := remotes
	for _, l := range locals {
		if !remote[strings.TrimPrefix(l, HeadRefPrefix)] {
			refs = append(refs, l)
		}
	}
	return refs, nil
}

// RefPublications lists the publications of one ref, tip first. With v == nil
// signatures are not checked at all; otherwise they are verified through v
// and untrusted publications are dropped.
func RefPublications(repoRoot, ref string, v *Verifier) ([]Publication, []string, error) {
	if v == nil {
		return listPublications(repoRoot, ref, nil, false)
	}
	pubs, warns, err := listPublications(repoRoot, ref, v.GitArgs(), v.Mode == VerifySigned)
	if err != nil {
		return nil, nil, err
	}
	trusted := pubs[:0]
	for _, p := range pubs {
		if v.Trusted(p) {
			trusted = append(trusted, p)
		}
	}
	return trusted, warns, nil
}
