package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/extract/rules"
	"github.com/alexdx2/chronicle-core/extract/structural"
	"github.com/alexdx2/chronicle-core/store"
)

// --- fixtures ---------------------------------------------------------------

const srDomain = "srapp"

const (
	srController = "src/billing.controller.ts"
	srService    = "src/billing.service.ts"
)

const srControllerTwoRoutes = `import { BillingService } from './billing.service';

@Controller('billing')
export class BillingController {
  constructor(private readonly billing: BillingService) {}

  @Get('invoices')
  list() { return this.billing.listInvoices(); }

  @Post('invoices')
  create(body: unknown) { return this.billing.createInvoice(body); }
}
`

const srControllerOneRoute = `import { BillingService } from './billing.service';

@Controller('billing')
export class BillingController {
  constructor(private readonly billing: BillingService) {}

  @Get('invoices')
  list() { return this.billing.listInvoices(); }
}
`

const srServiceSource = `import { Db } from './db';

@Injectable()
export class BillingService {
  listInvoices() { return []; }
}
`

// Node and edge keys the fixture resolves to.
const (
	srControllerNode = "code:controller:" + srDomain + ":src/billing-controller"
	srGetEndpoint    = "contract:endpoint:" + srDomain + ":get:/billing/invoices"
	srPostEndpoint   = "contract:endpoint:" + srDomain + ":post:/billing/invoices"
	srGetEdge        = srControllerNode + "->" + srGetEndpoint + ":EXPOSES_ENDPOINT"
	srPostEdge       = srControllerNode + "->" + srPostEndpoint + ":EXPOSES_ENDPOINT"
)

// srFiles is a mutable in-memory checkout: the tests rewrite a file between
// runs exactly as a commit would, and ReadFile reads from here.
type srFiles struct {
	content map[string]string
	fail    map[string]bool
	reads   map[string]int
}

func newSRFiles() *srFiles {
	return &srFiles{
		content: map[string]string{
			srController: srControllerTwoRoutes,
			srService:    srServiceSource,
		},
		fail:  map[string]bool{},
		reads: map[string]int{},
	}
}

func (f *srFiles) read(path string) ([]byte, error) {
	f.reads[path]++
	if f.fail[path] {
		return nil, fmt.Errorf("read %s: permission denied", path)
	}
	c, ok := f.content[path]
	if !ok {
		return nil, fmt.Errorf("read %s: no such file", path)
	}
	return []byte(c), nil
}

// srSetup opens a graph with one prior full scan, so the domain exists and the
// structural phase is genuinely an increment on an existing graph.
func srSetup(t *testing.T) (*Graph, *store.Store, *srFiles) {
	t.Helper()
	g, s, _ := setupTestGraph(t)
	if _, err := s.CreateRevision(srDomain, "", "base-sha", "manual", "full", "{}"); err != nil {
		t.Fatal(err)
	}
	return g, s, newSRFiles()
}

func srInput(f *srFiles, head string, changed ...string) StructuralInput {
	return StructuralInput{
		DomainKey: srDomain,
		HeadSHA:   head,
		Changed:   changed,
		Tech:      []string{"nestjs"},
		ReadFile:  f.read,
	}
}

func srRun(t *testing.T, g *Graph, in StructuralInput) *StructuralResult {
	t.Helper()
	res, err := g.StructuralRefresh(in)
	if err != nil {
		t.Fatalf("StructuralRefresh: %v", err)
	}
	return res
}

func srEdge(t *testing.T, s *store.Store, key string) *store.EdgeRow {
	t.Helper()
	e, err := s.GetEdgeByKey(key)
	if err != nil {
		t.Fatalf("GetEdgeByKey(%s): %v", key, err)
	}
	return e
}

// srEvidenceStatuses maps evidence id → status for one edge.
func srEdgeEvidence(t *testing.T, s *store.Store, key string) []store.EvidenceRow {
	t.Helper()
	e := srEdge(t, s, key)
	ev, err := s.ListEvidenceByEdge(e.EdgeID)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

// srHash is the content hash the phase records — sha256 of the bytes.
func srHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func srCountEvidence(t *testing.T, s *store.Store) int {
	t.Helper()
	var n int
	if err := s.QueryRowScan(`SELECT COUNT(*) FROM graph_evidence`, &n); err != nil {
		t.Fatal(err)
	}
	return n
}

// --- (a) structure for changed files ----------------------------------------

// The plain case: two changed TypeScript files become nodes and edges whose
// evidence says the structural extractor read them, and the phase's pointer
// names the commit it finished at.
func TestStructuralRefreshBuildsStructureForChangedFiles(t *testing.T) {
	g, s, f := srSetup(t)

	res := srRun(t, g, srInput(f, "head-1", srController, srService))

	if !res.Complete {
		t.Fatalf("phase must be complete when nothing is left over: %+v", res)
	}
	if res.Processed != 2 {
		t.Errorf("processed = %d, want 2", res.Processed)
	}
	if res.Facts == 0 {
		t.Errorf("facts = 0, want > 0")
	}
	if len(res.Failed) != 0 {
		t.Errorf("failed = %v, want none", res.Failed)
	}
	if res.Skipped != 0 || res.Backlog != 0 {
		t.Errorf("skipped = %d, backlog = %d, want 0/0", res.Skipped, res.Backlog)
	}

	// The structure itself.
	for _, key := range []string{srControllerNode, srGetEndpoint, srPostEndpoint} {
		if _, err := s.GetNodeByKey(key); err != nil {
			t.Errorf("node %s: %v", key, err)
		}
	}
	for _, key := range []string{srGetEdge, srPostEdge} {
		if e := srEdge(t, s, key); !e.Active {
			t.Errorf("edge %s is not active", key)
		}
	}

	// Its evidence is the structural extractor's, stamped with the rules pack.
	ev := srEdgeEvidence(t, s, srGetEdge)
	if len(ev) == 0 {
		t.Fatal("the route edge carries no evidence")
	}
	for _, e := range ev {
		if e.SourceKind != "ast" {
			t.Errorf("evidence source_kind = %q, want ast", e.SourceKind)
		}
		if e.ExtractorID != structural.ExtractorID {
			t.Errorf("evidence extractor_id = %q, want %s", e.ExtractorID, structural.ExtractorID)
		}
		if e.ExtractorVersion != rules.PackVersion {
			t.Errorf("evidence extractor_version = %q, want %s", e.ExtractorVersion, rules.PackVersion)
		}
	}

	// The pointer: this revision, and only because the phase finished.
	rev, err := s.LatestStructuralRevision(srDomain)
	if err != nil {
		t.Fatalf("LatestStructuralRevision: %v", err)
	}
	if rev.RevisionID != res.RevisionID || rev.GitAfterSHA != "head-1" {
		t.Fatalf("structural pointer = %+v, want revision %d at head-1", rev, res.RevisionID)
	}
	if !strings.Contains(rev.Metadata, `"pack":"`+rules.PackVersion+`"`) {
		t.Errorf("the stamp must name the rules pack: %s", rev.Metadata)
	}
}

// --- (b) honesty test 2: an endpoint removed from a surviving file ----------

// A successful extraction REPLACES the file's previous structural contribution.
// The route that is gone loses its evidence and its edge; the route that
// stayed is untouched; a declaration somebody else wrote about the same file is
// not the structural phase's to supersede; and a file that lost a route did not
// "fail", so it is not queued.
func TestStructuralRefreshSupersedesARouteTheFileNoLongerDeclares(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	// Somebody's product ruling about the same file, written by an importer.
	controller, err := s.GetNodeByKey(srControllerNode)
	if err != nil {
		t.Fatal(err)
	}
	declaredID, err := s.AddEvidence(store.EvidenceRow{
		TargetKind: "node", NodeID: controller.NodeID, SourceKind: "declared",
		FilePath: srController, LineStart: 1, ExtractorID: "owner",
		ExtractorVersion: "1", Confidence: 1, EvidenceStatus: "valid",
		EvidencePolarity: "positive", ValidFromRevisionID: 1, Metadata: "{}",
	})
	if err != nil {
		t.Fatal(err)
	}

	// The commit that deletes the POST route.
	f.content[srController] = srControllerOneRoute
	res := srRun(t, g, srInput(f, "head-2", srController))

	if len(res.Failed) != 0 {
		t.Fatalf("a removed route is not a parse failure: %v", res.Failed)
	}
	if res.Superseded == 0 {
		t.Errorf("superseded = 0, want the rows the file no longer asserts")
	}

	// The removed route: every structural row on it is superseded and the
	// edge is no longer current.
	for _, e := range srEdgeEvidence(t, s, srPostEdge) {
		if e.ExtractorID != structural.ExtractorID {
			continue
		}
		if e.EvidenceStatus != "superseded" {
			t.Errorf("POST route evidence %d status = %q, want superseded", e.EvidenceID, e.EvidenceStatus)
		}
	}
	if e := srEdge(t, s, srPostEdge); e.Active {
		t.Error("the removed route's edge is still active")
	}

	// The surviving route is untouched.
	keptValid := false
	for _, e := range srEdgeEvidence(t, s, srGetEdge) {
		if e.ExtractorID == structural.ExtractorID && (e.EvidenceStatus == "valid" || e.EvidenceStatus == "revalidated") {
			keptValid = true
		}
	}
	if !keptValid {
		t.Error("the route that stayed lost its evidence")
	}
	if e := srEdge(t, s, srGetEdge); !e.Active {
		t.Error("the route that stayed is no longer active")
	}

	// The declaration on the same file belongs to another writer.
	rows, err := s.ListEvidenceByNode(controller.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range rows {
		if e.EvidenceID != declaredID {
			continue
		}
		found = true
		if e.EvidenceStatus != "valid" {
			t.Errorf("declared evidence status = %q, want valid — not the structural phase's to supersede", e.EvidenceStatus)
		}
	}
	if !found {
		t.Fatalf("the declared row vanished from node %s", srControllerNode)
	}
}

// --- (c) same content twice -------------------------------------------------

// The phase is content-addressed: a file whose bytes and rules pack are the
// ones already recorded is not parsed again and writes nothing.
func TestStructuralRefreshSkipsUnchangedContent(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))
	before := srCountEvidence(t, s)

	res := srRun(t, g, srInput(f, "head-2", srController, srService))

	if res.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (both files unchanged): %+v", res.Skipped, res)
	}
	if res.Processed != 0 || res.Facts != 0 {
		t.Errorf("processed = %d, facts = %d, want 0/0", res.Processed, res.Facts)
	}
	if after := srCountEvidence(t, s); after != before {
		t.Errorf("evidence rows %d → %d; an unchanged file must write nothing", before, after)
	}
	if !res.Complete {
		t.Errorf("a run with nothing to do is still a complete run: %+v", res)
	}
}

// --- (d) a file that cannot be read ----------------------------------------

// A file the phase could not look at is not an emptiness: nothing it used to
// assert may be dropped. It is reported as UNREAD rather than as a parse
// failure — a model cannot read bytes that never arrived — and it stays in the
// retry set until somebody can.
func TestStructuralRefreshKeepsTheContributionOfAFileItCannotRead(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	f.fail[srController] = true
	f.content[srController] = srControllerOneRoute // the change it cannot see
	res := srRun(t, g, srInput(f, "head-2", srController))

	if len(res.Unread) != 1 || res.Unread[0] != srController {
		t.Fatalf("unread = %v, want [%s]", res.Unread, srController)
	}
	if len(res.Failed) != 0 {
		t.Errorf("a file that was never read did not fail to parse: %v", res.Failed)
	}
	// Both routes still stand: the phase did not read the file, so it knows
	// nothing new about it.
	for _, key := range []string{srGetEdge, srPostEdge} {
		if e := srEdge(t, s, key); !e.Active {
			t.Errorf("edge %s went inactive on a file that was never read", key)
		}
		valid := false
		for _, e := range srEdgeEvidence(t, s, key) {
			if e.ExtractorID == structural.ExtractorID && e.EvidenceStatus == "valid" {
				valid = true
			}
		}
		if !valid {
			t.Errorf("edge %s lost its valid structural evidence", key)
		}
	}
	// The content record still names the bytes the phase actually saw, so the
	// next readable run re-extracts. Recording the unread content would have
	// made the change invisible forever.
	if h, _, ok, err := s.GetStructuralHash(srDomain, srController); err != nil || !ok || h != srHash(srControllerTwoRoutes) {
		t.Errorf("recorded hash = %q (ok=%v, err=%v), want the content the phase last read", h, ok, err)
	}
}

// --- (e) rules-pack backlog -------------------------------------------------

// When the pack changes, files whose recorded look used an older one are
// re-extracted even though nothing in them changed — in bounded batches, and
// the pointer does not advance while any are left.
func TestStructuralRefreshDrainsTheOldPackBacklogInBatches(t *testing.T) {
	g, s, f := srSetup(t)
	third := "src/db.ts"
	f.content[third] = "export const db = 1;\n"

	// Three files last looked at under pack "0".
	for _, p := range []string{srController, srService, third} {
		if err := s.SetStructuralHash(srDomain, p, "stale-hash", "0", 1); err != nil {
			t.Fatal(err)
		}
	}

	in := srInput(f, "head-1")
	in.BacklogBatch = 2
	res := srRun(t, g, in)

	if res.Processed != 2 {
		t.Fatalf("processed = %d, want 2 (the batch size): %+v", res.Processed, res)
	}
	if res.Backlog != 1 {
		t.Fatalf("backlog = %d, want 1 file still on the old pack", res.Backlog)
	}
	if res.Complete {
		t.Fatal("structured must not advance while the backlog is not drained")
	}
	if _, err := s.LatestStructuralRevision(srDomain); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("an undrained backlog must leave no structural pointer: %v", err)
	}

	// The next run finishes the job.
	in2 := srInput(f, "head-1")
	in2.BacklogBatch = 2
	res2 := srRun(t, g, in2)
	if res2.Processed != 1 || res2.Backlog != 0 || !res2.Complete {
		t.Fatalf("second run: processed=%d backlog=%d complete=%v, want 1/0/true",
			res2.Processed, res2.Backlog, res2.Complete)
	}
	rev, err := s.LatestStructuralRevision(srDomain)
	if err != nil {
		t.Fatalf("the drained run must pin the commit: %v", err)
	}
	if rev.GitAfterSHA != "head-1" {
		t.Fatalf("structural pointer at %s, want head-1", rev.GitAfterSHA)
	}
}

// --- (f) the phase dies halfway ---------------------------------------------

// A phase that did not finish claims nothing. The revision it was working on
// stands (the verification phase may have earned it), its structural stamp
// says incomplete, and the previous completed phase is still the pointer.
func TestStructuralRefreshLeavesThePointerBehindWhenItFails(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	boom := errors.New("resolver exploded")
	orig := structuralResolveFn
	structuralResolveFn = func(*Graph, string, int64, ResolveOptions) (*ResolveExtractionsResult, error) {
		return nil, boom
	}
	f.content[srController] = srControllerOneRoute
	_, err := g.StructuralRefresh(srInput(f, "head-2", srController))
	structuralResolveFn = orig
	if !errors.Is(err, boom) {
		t.Fatalf("StructuralRefresh error = %v, want the resolver's", err)
	}

	// The revision at head-2 exists and says the phase did not finish.
	failedRev, err := s.GetRevisionBySHA(srDomain, "head-2")
	if err != nil {
		t.Fatalf("the revision the phase was working on must stand: %v", err)
	}
	if !strings.Contains(failedRev.Metadata, `"complete":false`) {
		t.Errorf("an interrupted phase must say so: %s", failedRev.Metadata)
	}
	// And the pointer is still the last phase that DID finish.
	rev, err := s.LatestStructuralRevision(srDomain)
	if err != nil {
		t.Fatalf("LatestStructuralRevision: %v", err)
	}
	if rev.GitAfterSHA != "head-1" {
		t.Fatalf("structural pointer moved to %s; a failed phase guarantees nothing", rev.GitAfterSHA)
	}
	// The new content was never applied, so it was never recorded — the
	// rerun redoes the file instead of skipping it as "already done".
	if h, _, _, _ := s.GetStructuralHash(srDomain, srController); h == srHash(srControllerOneRoute) {
		t.Error("a file whose extraction was never applied must not be recorded as done")
	}

	// Rerun: same commit, same revision, and now it completes.
	res := srRun(t, g, srInput(f, "head-2", srController))
	if !res.Complete {
		t.Fatalf("the rerun must complete: %+v", res)
	}
	if res.RevisionID != failedRev.RevisionID {
		t.Errorf("rerun revision = %d, want the existing %d at head-2", res.RevisionID, failedRev.RevisionID)
	}
	rev, err = s.LatestStructuralRevision(srDomain)
	if err != nil || rev.GitAfterSHA != "head-2" {
		t.Fatalf("after the rerun the pointer must be head-2: %+v (%v)", rev, err)
	}
	if e := srEdge(t, s, srPostEdge); e.Active {
		t.Error("the rerun must still apply the replacement contract")
	}
}

// --- deletions --------------------------------------------------------------

// A deleted file's structural contribution is closed the same way a replaced
// one is, and its content record is forgotten so a file that comes back is
// looked at again.
func TestStructuralRefreshClosesDeletedFiles(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	delete(f.content, srController)
	in := srInput(f, "head-2")
	in.Deleted = []string{srController}
	res := srRun(t, g, in)

	if res.Superseded == 0 {
		t.Errorf("a deleted file's rows must be closed, superseded = 0")
	}
	if e := srEdge(t, s, srGetEdge); e.Active {
		t.Error("a route of a deleted file is still active")
	}
	if _, _, ok, _ := s.GetStructuralHash(srDomain, srController); ok {
		t.Error("the deleted file is still recorded as looked at")
	}
	if !res.Complete {
		t.Errorf("a deletion-only run is a complete run: %+v", res)
	}
}

// --- the revision the phase lands on ----------------------------------------

// The stamp rides on whatever revision already sits at HEAD —
// UNIQUE(domain_key, git_after_sha) allows nothing else — and the phase
// creates a revision of its own only when that commit is unclaimed.
func TestStructuralRefreshStampsTheRevisionAlreadyAtHead(t *testing.T) {
	g, s, f := srSetup(t)

	refreshID, err := s.CreateRevision(srDomain, "", "head-1", "git_hook", "incremental", `{"kind":"refresh"}`)
	if err != nil {
		t.Fatal(err)
	}
	res := srRun(t, g, srInput(f, "head-1", srController))
	if res.RevisionID != refreshID {
		t.Fatalf("phase revision = %d, want the refresh's %d", res.RevisionID, refreshID)
	}
	rev, err := s.GetRevision(refreshID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rev.Metadata, `"kind":"refresh"`) {
		t.Errorf("the stamp erased what the refresh recorded: %s", rev.Metadata)
	}
	if !strings.Contains(rev.Metadata, `"complete":true`) {
		t.Errorf("the stamp is missing: %s", rev.Metadata)
	}

	// An unclaimed commit gets a structure-only revision, marked so the
	// verification pointer never mistakes it for a re-verification.
	res2 := srRun(t, g, srInput(f, "head-2"))
	own, err := s.GetRevision(res2.RevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if own.TriggerKind != "git_hook" || !strings.Contains(own.Metadata, `"kind":"structural"`) {
		t.Fatalf("structure-only revision = %+v", own)
	}
	// It is a git_hook row, so only metadata.kind keeps it from being read as
	// the last re-verification — which would make the graph claim it checked
	// evidence at head-2 that nobody looked at.
	verified, err := s.LatestRefreshRevision(srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if verified.RevisionID != refreshID {
		t.Errorf("verified pointer = %d (%s), want the refresh at head-1 — a structure-only revision verified nothing",
			verified.RevisionID, verified.GitAfterSHA)
	}
}

// A deterministic resolve answers for no agent: the obligation gate exists to
// stop a scan resolving before its agents finished their files, and the
// structural phase asks nobody for anything. Without this, phase 2 could never
// run on the revision phase 1 opened (whose verify_file obligations are still
// open by design).
func TestStructuralRefreshRunsOnARevisionWithOpenObligations(t *testing.T) {
	g, s, f := srSetup(t)

	revID, err := s.CreateRevision(srDomain, "", "head-1", "git_hook", "incremental", `{"kind":"refresh"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateObligation(revID, srDomain, "verify_file", "lib/thing.rb", "stale evidence"); err != nil {
		t.Fatal(err)
	}

	res := srRun(t, g, srInput(f, "head-1", srController, srService))
	if !res.Complete || res.Processed != 2 {
		t.Fatalf("the phase must run beside an open verification obligation: %+v", res)
	}
	open, err := s.ListOpenObligations(revID)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("the phase must neither satisfy nor fail somebody else's obligation: %d open", len(open))
	}
}

// Two writers on one revision. The structural phase resolves on the revision
// the refresh — or a scan — already opened, and it may only build the graph
// from facts it read itself. An agent's extraction on that revision is not its
// input and not its to close.
func TestStructuralRefreshLeavesAnotherWritersExtractionAlone(t *testing.T) {
	g, s, f := srSetup(t)

	revID, err := s.CreateRevision(srDomain, "", "head-1", "git_hook", "incremental", `{"kind":"refresh"}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveExtraction(revID, srDomain, "src/agent-read.ts", "extracted", "provider",
		`[{"kind":"import","to":"./ghost","symbols":["Ghost"]}]`, ""); err != nil {
		t.Fatal(err)
	}

	srRun(t, g, srInput(f, "head-1", srController, srService))

	left, err := s.ListUnresolvedExtractions(revID, srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].FilePath != "src/agent-read.ts" {
		t.Fatalf("the agent's row was consumed by the structural resolve: %+v", left)
	}
	if _, err := s.GetNodeByKey("code:provider:" + srDomain + ":src/ghost"); err == nil {
		t.Error("the structural phase built the graph from facts it never read")
	}
}

// The graph-wide post-passes cost time proportional to the domain, and a
// per-commit phase must not pay it. Flow derivation and service containment do
// not run; the passes that follow the files just read still do.
func TestStructuralRefreshSkipsTheGraphWidePostPasses(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	nodes, err := s.ListNodes(store.NodeFilter{Domain: srDomain})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range nodes {
		if n.Layer == "flow" {
			t.Errorf("a per-commit phase derived a flow node: %s", n.NodeKey)
		}
	}
	edges, err := s.ListEdges(store.EdgeFilter{})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		switch e.EdgeType {
		case "TRIGGERS_FLOW", "REQUIRES", "CONTAINS":
			t.Errorf("a per-commit phase ran a derived pass: %s (%s)", e.EdgeKey, e.EdgeType)
		}
	}
	// What the files themselves said is still there.
	if _, err := s.GetEdgeByKey(srGetEdge); err != nil {
		t.Fatalf("the route the file declares is missing: %v", err)
	}

	// And a scan-shaped resolve on the same graph still derives everything.
	revID, err := s.CreateRevision(srDomain, "", "scan-2", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveExtraction(revID, srDomain, srController, "extracted", "controller",
		`[{"kind":"endpoint","from":"billing","method":"GET","target":"invoices"}]`, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := g.ResolveExtractions(srDomain, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}
	nodes, _ = s.ListNodes(store.NodeFilter{Domain: srDomain})
	flows := 0
	for _, n := range nodes {
		if n.Layer == "flow" {
			flows++
		}
	}
	if flows == 0 {
		t.Error("a scan's resolve must still derive flows — the gate is per-call, not global")
	}
}

// A rerun at a commit whose phase already finished must not withdraw the claim
// while it works. The "in progress" marker exists to stop an unfinished run
// claiming something untrue — not to drop something true: a pack bump that
// crashes halfway would otherwise leave the commit looking unstructured when
// its structure is exactly as good as it was a second earlier.
func TestStructuralRefreshDoesNotWithdrawACompletedClaimOnARerun(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))
	if rev, err := s.LatestStructuralRevision(srDomain); err != nil || rev.GitAfterSHA != "head-1" {
		t.Fatalf("structured@%+v (%v), want head-1", rev, err)
	}

	// The same commit again — and this time the resolver dies.
	boom := errors.New("resolver exploded")
	orig := structuralResolveFn
	structuralResolveFn = func(*Graph, string, int64, ResolveOptions) (*ResolveExtractionsResult, error) {
		return nil, boom
	}
	f.content[srController] = srControllerOneRoute
	_, err := g.StructuralRefresh(srInput(f, "head-1", srController))
	structuralResolveFn = orig
	if !errors.Is(err, boom) {
		t.Fatalf("StructuralRefresh error = %v, want the resolver's", err)
	}

	rev, err := s.LatestStructuralRevision(srDomain)
	if err != nil {
		t.Fatalf("a finished phase's claim was withdrawn by a failed rerun: %v", err)
	}
	if rev.GitAfterSHA != "head-1" {
		t.Fatalf("structured@%s, want head-1", rev.GitAfterSHA)
	}
}

// The content record names the revision that wrote it, so a rebase can forget
// exactly the files whose structure came from a branch nobody merged.
func TestStructuralRefreshRecordsTheRevisionWithTheContentHash(t *testing.T) {
	g, s, f := srSetup(t)
	res := srRun(t, g, srInput(f, "head-1", srController, srService))

	if n, err := s.DeleteStructuralHashesAfter(srDomain, res.RevisionID); err != nil || n != 0 {
		t.Fatalf("records written BY this revision are not after it: %d (%v)", n, err)
	}
	n, err := s.DeleteStructuralHashesAfter(srDomain, res.RevisionID-1)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("forgot %d records, want the 2 this revision wrote", n)
	}
}

// The other half of that gate: the passes a scan DOES run must still run in
// the order they were written in.
//
// mergeExternalSystemsIntoServices repoints the external_system placeholder an
// http_call minted for a host that turned out to be this domain's own service,
// and deletes it. materializeExternalEndpoints then mints a boundary endpoint
// node for whatever is STILL external — it reads the placeholder's status to
// decide. Materialising first makes the graph publish an `external` contract
// for a call inside the domain, whose endpoint already exists locally.
func TestScanPathMergesExternalSystemsBeforeMaterialisingEndpoints(t *testing.T) {
	g, s, _ := setupTestGraph(t)
	const dom = "ordapp"
	revID, err := s.CreateRevision(dom, "", "ord1", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	// The domain's own service, known from an earlier scan.
	if _, err := s.UpsertNode(store.NodeRow{
		NodeKey: "service:service:" + dom + ":jerry-api", Layer: "service", NodeType: "service",
		DomainKey: dom, Name: "jerry-api", Status: "active",
		FirstSeenRevisionID: revID, LastSeenRevisionID: revID, Confidence: 1, Metadata: "{}",
	}); err != nil {
		t.Fatal(err)
	}
	// A controller calling it over HTTP by host name.
	if _, err := s.SaveExtraction(revID, dom, "src/tom.controller.ts", "extracted", "controller",
		`[{"kind":"http_call","method":"GET","target":"http://jerry-api:3002/jerry/status","from_type":"controller"}]`,
		""); err != nil {
		t.Fatal(err)
	}

	if _, err := g.ResolveExtractions(dom, revID); err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	// The call landed on the real service, and the placeholder is gone.
	if _, err := s.GetEdgeByKey("code:controller:" + dom + ":src/tom-controller->service:service:" + dom + ":jerry-api:CALLS_SERVICE"); err != nil {
		t.Fatalf("the call was not repointed at the in-domain service: %v", err)
	}
	if n, err := s.GetNodeByKey("service:external_system:" + dom + ":jerry-api"); err == nil && n.Status == "active" {
		t.Error("the external_system placeholder outlived the merge")
	}
	// And no boundary contract was published for it.
	if n, err := s.GetNodeByKey("contract:endpoint:" + dom + ":get:/jerry/status"); err == nil {
		t.Errorf("an in-domain call published an %q endpoint node — materialise ran before merge", n.Status)
	}
	edges, err := s.ListEdges(store.EdgeFilter{EdgeType: "CALLS_ENDPOINT"})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range edges {
		if strings.Contains(e.ToNodeKey, "/jerry/status") {
			t.Errorf("an in-domain call gained a boundary CALLS_ENDPOINT edge: %s", e.EdgeKey)
		}
	}
}

// Role scoping has to run both ways. The structural phase keeps ONE standing
// row per file, on whatever revision it last resolved; a scan that opens a
// revision those rows already point at must not resolve them, close them, or
// count them as its own work — it never read those files in this run.
func TestAScanDoesNotResolveOrCountTheStructuralPhasesRows(t *testing.T) {
	g, s, f := srSetup(t)
	// A completed structural phase at a commit.
	res := srRun(t, g, srInput(f, "head-1", srController, srService))

	// The scan opens its own revision and reads one file.
	scanRev, err := s.CreateRevision(srDomain, "", "scan-head", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveExtraction(scanRev, srDomain, "src/agent-only.ts", "extracted", "provider",
		`[{"kind":"import","to":"./thing","symbols":["Thing"]}]`, ""); err != nil {
		t.Fatal(err)
	}
	// The phase's rows are re-pointed at the scan's revision by a run there —
	// the state a scan at the same commit as the last refresh actually finds.
	if _, err := s.SaveStructuralExtraction(scanRev, srDomain, srController, "extracted", "controller",
		`[{"kind":"endpoint","from":"billing","method":"DELETE","target":"invoices"}]`, "", ""); err != nil {
		t.Fatal(err)
	}

	out, err := g.ResolveExtractions(srDomain, scanRev)
	if err != nil {
		t.Fatalf("ResolveExtractions: %v", err)
	}

	if out.FilesProcessed != 1 {
		t.Errorf("the scan resolved %d files, want 1 — its own", out.FilesProcessed)
	}
	if out.ExtractionsResolved != 1 {
		t.Errorf("the scan closed %d rows, want 1", out.ExtractionsResolved)
	}
	// The phase's row is untouched, and the route it holds was not built.
	left, err := s.ListUnresolvedExtractionsByRole(scanRev, srDomain, store.StructuralExtractionRole)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].Status != "extracted" {
		t.Fatalf("the structural row was consumed by the scan: %+v", left)
	}
	if _, err := s.GetNodeByKey("contract:endpoint:" + srDomain + ":delete:/billing/invoices"); err == nil {
		t.Error("the scan built the graph from the structural phase's facts")
	}

	// And the scan's own coverage does not count them.
	n, err := g.CountResolvedExtractions(scanRev, srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("CountResolvedExtractions = %d, want 1 — the phase's row is not the scan's work", n)
	}
	_ = res
}

// A file with no answer is retried on every run, and nothing else would bring
// it back: a failure writes no content record, so it is not in the pack
// backlog, and it has stopped changing, so it is not in the diff. Without the
// retry set a transient read error is a file the graph forgets permanently.
func TestStructuralRefreshRetriesAFileWithNoStandingAnswer(t *testing.T) {
	g, s, f := srSetup(t)
	f.fail[srController] = true
	res := srRun(t, g, srInput(f, "head-1", srController, srService))
	if len(res.Unread) != 1 {
		t.Fatalf("fixture: unread = %v", res.Unread)
	}
	if _, err := s.GetNodeByKey(srGetEndpoint); err == nil {
		t.Fatal("fixture: the unread file must have contributed nothing")
	}

	// A later commit that does not mention the file at all.
	f.fail[srController] = false
	res2 := srRun(t, g, srInput(f, "head-2"))

	if res2.Processed != 1 {
		t.Fatalf("processed = %d, want the retried file: %+v", res2.Processed, res2)
	}
	if len(res2.Unread) != 0 || len(res2.Failed) != 0 {
		t.Fatalf("the retry succeeded but was still reported as a failure: %+v", res2)
	}
	if _, err := s.GetNodeByKey(srGetEndpoint); err != nil {
		t.Fatalf("the retried file's structure never arrived: %v", err)
	}
	// It leaves the retry set once it has an answer.
	failed, unread, err := s.StructuralFailures(srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 0 || len(unread) != 0 {
		t.Errorf("still in the retry set: failed=%v unread=%v", failed, unread)
	}
}

// A deleted file must leave the retry set with everything else, or its
// standing failure spends a batch slot every run on a file that cannot return.
func TestStructuralRefreshForgetsAFailedFileWhenItIsDeleted(t *testing.T) {
	g, s, f := srSetup(t)
	f.fail[srController] = true
	srRun(t, g, srInput(f, "head-1", srController, srService))
	if _, unread, _ := s.StructuralFailures(srDomain); len(unread) != 1 {
		t.Fatalf("fixture: unread = %v", unread)
	}

	delete(f.content, srController)
	in := srInput(f, "head-2")
	in.Deleted = []string{srController}
	srRun(t, g, in)

	failed, unread, err := s.StructuralFailures(srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed) != 0 || len(unread) != 0 {
		t.Errorf("a deleted file is still owed an answer: failed=%v unread=%v", failed, unread)
	}
}

// The guard against withdrawing a finished claim has to read `complete` the
// way the POINTER reads it. SQLite's JSON1 maps a JSON true to the integer 1
// and LatestStructuralRevision accepts `= 1`, so a stamp written as 1 is a
// completed phase to the graph — and must be one here too, or a revision the
// graph calls complete still loses its claim to a crashed rerun.
func TestStructuralAlreadyCompleteAgreesWithThePointer(t *testing.T) {
	g, s, _ := srSetup(t)
	for _, tc := range []struct {
		metadata string
		want     bool
	}{
		{`{"structural":{"complete":true}}`, true},
		{`{"structural":{"complete":1}}`, true},
		{`{"structural":{"complete":false}}`, false},
		{`{"structural":{"complete":0}}`, false},
		{`{"kind":"refresh"}`, false},
		{`not json`, false},
	} {
		id, err := s.CreateRevision(srDomain, "", "sha-"+tc.metadata, "git_hook", "incremental", tc.metadata)
		if err != nil {
			t.Fatal(err)
		}
		got, err := g.structuralAlreadyComplete(id)
		if err != nil {
			t.Fatalf("%s: %v", tc.metadata, err)
		}
		if got != tc.want {
			t.Errorf("structuralAlreadyComplete(%s) = %v, want %v", tc.metadata, got, tc.want)
		}
		// And the pointer's own reading, for the two that claim completion.
		rev, perr := s.LatestStructuralRevision(srDomain)
		pointerSees := perr == nil && rev.RevisionID == id
		if tc.want != pointerSees {
			t.Errorf("%s: guard says %v, the pointer says %v", tc.metadata, tc.want, pointerSees)
		}
	}
}

const srModuleSource = `import { BillingService } from './billing.service';

@Module({
  providers: [BillingService],
})
export class AppModule {}
`

const srBootSource = `import { AppModule } from './app.module';

@Injectable()
export class Bootstrapper {
  boot() { return AppModule; }
}
`

// One path is one node. The file index built from this pass's extractions is
// what tells an import which TYPE the file it points at turned out to be; with
// an empty index the import falls back to guessing from the class name, and a
// module imported by anything lands as a second, provider-typed node for the
// same path — two nodes, one file, and every query about it answering half.
func TestStructuralRefreshDoesNotMintAMistypedTwinForAPath(t *testing.T) {
	g, s, f := srSetup(t)
	f.content["src/app.module.ts"] = srModuleSource
	f.content["src/boot.ts"] = srBootSource

	srRun(t, g, srInput(f, "head-1", "src/app.module.ts", "src/boot.ts", srService))

	nodes, err := s.ListNodes(store.NodeFilter{Domain: srDomain})
	if err != nil {
		t.Fatal(err)
	}
	var forModule []store.NodeRow
	for _, n := range nodes {
		if strings.HasSuffix(n.NodeKey, ":src/app-module") {
			forModule = append(forModule, n)
		}
	}
	if len(forModule) != 1 {
		keys := make([]string, 0, len(forModule))
		for _, n := range forModule {
			keys = append(keys, n.NodeKey)
		}
		t.Fatalf("%d nodes for src/app.module.ts: %v — one path is one node", len(forModule), keys)
	}
	if forModule[0].NodeType != "module" {
		t.Errorf("node type = %q, want module — the file said so", forModule[0].NodeType)
	}
}

// The retry set is bounded by the same number that bounds everything else.
// Uncapped, files the parser can never read would add themselves to EVERY run
// and crowd the pack backlog out from behind them forever.
func TestStructuralRefreshCapsTheRetrySetAtTheBatch(t *testing.T) {
	g, s, f := srSetup(t)
	// Three files with no standing answer.
	for i, name := range []string{"src/x1.ts", "src/x2.ts", "src/x3.ts"} {
		f.content[name] = srServiceSource
		f.fail[name] = true
		_ = i
	}
	in := srInput(f, "head-1", "src/x1.ts", "src/x2.ts", "src/x3.ts")
	res := srRun(t, g, in)
	if len(res.Unread) != 3 {
		t.Fatalf("fixture: unread = %v", res.Unread)
	}

	// A later run that mentions none of them, with room for two.
	for _, name := range []string{"src/x1.ts", "src/x2.ts", "src/x3.ts"} {
		f.fail[name] = false
	}
	in2 := srInput(f, "head-2")
	in2.BacklogBatch = 2
	f.reads = map[string]int{}
	res2 := srRun(t, g, in2)

	if res2.Processed != 2 {
		t.Fatalf("processed = %d, want 2: %+v", res2.Processed, res2)
	}
	// The cap is about what the run TOUCHES, not only what it parses: an
	// uncapped retry set is read and hashed in full every run even when the
	// parse budget stops short, which is the cost a hook pays.
	if len(f.reads) != 2 {
		t.Fatalf("the run read %d files (%v), want the 2 the batch has room for", len(f.reads), f.reads)
	}
	failed, unread, err := s.StructuralFailures(srDomain)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed)+len(unread) != 1 {
		t.Fatalf("%d files still without an answer, want the 1 the batch had no room for", len(failed)+len(unread))
	}
	// And the next run takes it.
	in3 := srInput(f, "head-3")
	in3.BacklogBatch = 2
	if res3 := srRun(t, g, in3); res3.Processed != 1 {
		t.Fatalf("third run processed %d, want the last one: %+v", res3.Processed, res3)
	}
}
