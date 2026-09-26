package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/alexdx2/chronicle-core/snapshot"
	"github.com/spf13/cobra"
)

// newGraphMergeCmd wires `chronicle graph merge <base> <ours> <theirs>` — a
// git merge-driver, invoked BY git with three temp file paths it created
// (the %O/%A/%B convention registered in .git/config by ensureGitWiring),
// not repo-relative paths and not revisions. It is not meant to be typed by
// a person, hence Hidden.
func newGraphMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "merge <base> <ours> <theirs>",
		Short:  "Git merge-driver for graph.jsonl (registered by init/attach — not for interactive use)",
		Hidden: true,
		Args:   cobra.ExactArgs(3),
		Run: func(cmd *cobra.Command, args []string) {
			runGraphMerge(args[0], args[1], args[2], os.Stderr)
			// Deliberately no os.Exit / returned error: a git merge-driver
			// MUST report success. Exiting non-zero here makes git fall back
			// to a text merge with conflict markers dropped straight into
			// graph.jsonl — exactly what this feature exists to avoid (see
			// snapshot.Merge's doc comment and design spec §6.5, "conflict =
			// rebuild, not text merge"). Problems go to warn instead.
		},
	}
}

// runGraphMerge is the testable core of the merge driver: decode base/ours/
// theirs, run the keyed three-way merge, and write the result back over
// oursPath (git's %A — the file git will use as the merge resolution).
// Never returns an error for the caller to act on; everything short of an
// unwritable oursPath degrades to a best-effort result plus a line on warn.
func runGraphMerge(basePath, oursPath, theirsPath string, warn io.Writer) {
	base := decodeMergeInput(basePath, warn)
	ours := decodeMergeInput(oursPath, warn)
	theirs := decodeMergeInput(theirsPath, warn)

	merged := snapshot.Merge(base, ours, theirs)

	f, err := os.Create(oursPath)
	if err != nil {
		fmt.Fprintf(warn, "chronicle graph merge: writing %s: %v\n", oursPath, err)
		return
	}
	defer f.Close()
	if err := snapshot.Encode(f, merged); err != nil {
		fmt.Fprintf(warn, "chronicle graph merge: encoding merged result: %v\n", err)
	}
}

// decodeMergeInput reads and decodes one merge-driver input file. This
// content comes from a branch being merged — the same trust level as CI
// review input — so it is decoded with DefaultLimits, the generous bound
// package snapshot already uses for exactly this "untrusted external
// snapshot" case (snapshot.AtCommit, reading graph.jsonl out of arbitrary
// git history, uses the same constructor for the same reason).
//
// A missing file or a fatal decode error (unrecognized format/version)
// degrades to an empty snapshot with a warning rather than aborting: the
// merge driver's contract is to always produce SOME merged result.
func decodeMergeInput(path string, warn io.Writer) *snapshot.Snapshot {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(warn, "chronicle graph merge: reading %s: %v (treating as empty)\n", path, err)
		return &snapshot.Snapshot{}
	}
	snap, warnings, err := snapshot.Decode(bytes.NewReader(data), snapshot.DefaultLimits())
	if err != nil {
		fmt.Fprintf(warn, "chronicle graph merge: decoding %s: %v (treating as empty)\n", path, err)
		return &snapshot.Snapshot{}
	}
	for _, w := range warnings {
		fmt.Fprintf(warn, "chronicle graph merge: %s:%d: %s: %s\n", path, w.Line, w.Code, w.Msg)
	}
	return snap
}
