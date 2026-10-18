package review

import (
	"context"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/snapshot"
)

// topicRenameFixture: one producer publishes battle-results; scoreboard-api
// and spectators-api consume it. In head spectators-api's consumer moves to
// battle-results-v2 (no publisher). battle-results itself stays in both
// graphs (the producer and the scoreboard consumer still reference it).
func topicRenameFixture(t *testing.T, extraHeadEdges ...snapshot.Edge) *ReviewResult {
	t.Helper()
	dir := initGitRepoT(t)
	scopedManifestT(t, dir, "zzz-nomatch/**")

	mk := func(id, layer, typ, name, file string) snapshot.Node {
		return snapshot.Node{Kind: "node", ID: id, Layer: layer, Type: typ, Domain: "tj", Name: name, File: file, Status: "active"}
	}
	svcArena := mk("service:service:tj:arena-api", "service", "service", "arena-api", "")
	svcScore := mk("service:service:tj:scoreboard-api", "service", "service", "scoreboard-api", "")
	svcSpect := mk("service:service:tj:spectators-api", "service", "service", "spectators-api", "")
	producer := mk("code:provider:tj:arena-api/producer", "code", "provider", "BattleResultProducer", "arena-api/producer.ts")
	scoreC := mk("code:provider:tj:scoreboard-api/consumer", "code", "provider", "BattleResultConsumer", "scoreboard-api/consumer.ts")
	spectC := mk("code:provider:tj:spectators-api/consumer", "code", "provider", "BattleResultConsumer", "spectators-api/consumer.ts")
	topic := mk("contract:topic:tj:battle-results", "contract", "topic", "battle-results", "")
	topicV2 := mk("contract:topic:tj:battle-results-v2", "contract", "topic", "battle-results-v2", "")

	edge := func(from, to, typ string) snapshot.Edge {
		return snapshot.Edge{Kind: "edge", ID: from + "->" + to + ":" + typ, Type: typ, From: from, To: to, Active: true, Derivation: "hard"}
	}
	pub := edge(producer.ID, topic.ID, "PUBLISHES_TOPIC")
	scoreConsumes := edge(scoreC.ID, topic.ID, "CONSUMES_TOPIC")
	spectConsumes := edge(spectC.ID, topic.ID, "CONSUMES_TOPIC")
	spectConsumesV2 := edge(spectC.ID, topicV2.ID, "CONSUMES_TOPIC")

	hdr := func(seq int) snapshot.Header {
		return snapshot.Header{Kind: "header", Format: snapshot.Format, Version: snapshot.Version, Seq: seq, FactSchema: snapshot.FactSchema, Domains: []string{"tj"}}
	}
	common := []snapshot.Node{svcArena, svcScore, svcSpect, producer, scoreC, spectC, topic}
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, &snapshot.Snapshot{
		Header: hdr(1), Nodes: common, Edges: []snapshot.Edge{pub, scoreConsumes, spectConsumes},
	}))
	baseSHA := commitAllT(t, dir, "base")
	headEdges := append([]snapshot.Edge{pub, scoreConsumes, spectConsumesV2}, extraHeadEdges...)
	writeFileT(t, dir, ".depbot/graph.jsonl", encodeSnapshotT(t, &snapshot.Snapshot{
		Header: hdr(2), Nodes: append(append([]snapshot.Node{}, common...), topicV2), Edges: headEdges,
	}))
	headSHA := commitAllT(t, dir, "head")

	res, err := Review(context.Background(), ReviewInput{
		BaseSource: SnapshotSource{RepoRoot: dir, Rev: baseSHA},
		HeadSource: SnapshotSource{RepoRoot: dir, Rev: headSHA},
		RepoRoot:   dir,
		Depth:      4,
	})
	if err != nil {
		t.Fatalf("Review: %v", err)
	}
	return res
}

func TestReview_TopicRename_UnchangedConsumerNotBroken(t *testing.T) {
	res := topicRenameFixture(t)

	for _, f := range res.Findings {
		if f.Kind == ContractRemoved {
			t.Errorf("unexpected ContractRemoved: %+v", f)
		}
	}
	for _, a := range res.WhoMayBeAffected() {
		if strings.Contains(a.System.Key, "scoreboard-api") {
			t.Errorf("scoreboard-api (consumer unchanged) is affected: %+v", a)
		}
	}

	var added *ReviewFinding
	for i, f := range res.Findings {
		if f.Kind == IntegrationAdded && strings.Contains(f.Counterpart, "battle-results-v2") {
			added = &res.Findings[i]
		}
	}
	if added == nil {
		t.Fatalf("no IntegrationAdded for battle-results-v2 in %+v", res.Findings)
	}
	if added.Reason != ReasonNewSubscriptionNoPublisher {
		t.Errorf("Reason = %q, want %q", added.Reason, ReasonNewSubscriptionNoPublisher)
	}
	if len(onlySeverity(added.Downstream, SeverityAffected)) != 0 {
		t.Errorf("moved consumer has Affected downstream: %+v", added.Downstream)
	}
	if info := onlySeverity(added.Downstream, SeverityInformational); len(info) != 1 || info[0].Reason != ReasonNewSubscriptionNoPublisher {
		t.Errorf("Informational downstream = %+v, want one with the no-publisher reason", info)
	}
	if got := effectPhrase(*added); !strings.Contains(got, "new subscription has no publisher") {
		t.Errorf("effect phrase = %q", got)
	}
}

func TestReview_DanglingEdgeSurfacesAsWarning(t *testing.T) {
	dangling := snapshot.Edge{Kind: "edge", ID: "code:provider:tj:arena-api/producer->contract:topic:tj:ghost:PUBLISHES_TOPIC",
		Type: "PUBLISHES_TOPIC", From: "code:provider:tj:arena-api/producer", To: "contract:topic:tj:ghost", Active: true, Derivation: "hard"}
	res := topicRenameFixture(t, dangling)

	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "head graph") || !strings.Contains(res.Warnings[0], "relationship(s) dropped") {
		t.Fatalf("Warnings = %q, want one head-graph dangling warning", res.Warnings)
	}
	md, err := RenderMarkdown(res, MarkdownOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(md, "### Limitations") || !strings.Contains(md, "relationship(s) dropped") {
		t.Errorf("markdown does not show the limitation:\n%s", md)
	}
}
