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

// A file the phase could not look at is a failure, not an emptiness: nothing
// it used to assert may be dropped, and it is named so the semantic queue can
// pick it up.
func TestStructuralRefreshKeepsTheContributionOfAFileItCannotRead(t *testing.T) {
	g, s, f := srSetup(t)
	srRun(t, g, srInput(f, "head-1", srController, srService))

	f.fail[srController] = true
	f.content[srController] = srControllerOneRoute // the change it cannot see
	res := srRun(t, g, srInput(f, "head-2", srController))

	if len(res.Failed) != 1 || res.Failed[0] != srController {
		t.Fatalf("failed = %v, want [%s]", res.Failed, srController)
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
		if err := s.SetStructuralHash(srDomain, p, "stale-hash", "0"); err != nil {
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
