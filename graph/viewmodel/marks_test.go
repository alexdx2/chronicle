package viewmodel

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/store"
)

const (
	mkShopSvc  = "service:service:d:shop-api"
	mkMailSvc  = "service:service:d:mail-api"
	mkShopCtl  = "code:controller:d:shop-api/src/shop.controller"
	mkShopProv = "code:provider:d:shop-api/src/shop.service"
	mkKafka    = "infra:broker:d:kafka"
	mkRedis    = "infra:cache:d:redis"
	mkTopic    = "contract:topic:d:order-created"
	mkCall     = mkShopProv + "->" + mkMailSvc + ":CALLS_SERVICE"
	mkInjects  = mkShopCtl + "->" + mkShopProv + ":INJECTS"
	mkEndpoint = "contract:endpoint:d:get:/orders"
)

func markTestHas(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// synthWithEndpoint extends synthStore with an endpoint the shop controller
// exposes, so a view can absorb it as a pill.
func synthWithEndpoint(t *testing.T) *store.Store {
	t.Helper()
	st := synthStore(t)
	ctl, err := st.GetNodeByKey(mkShopCtl)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := st.CreateRevision("d", "", "marks-endpoint", "manual", "full", "{}")
	if err != nil {
		t.Fatal(err)
	}
	epID, err := st.UpsertNode(store.NodeRow{NodeKey: mkEndpoint, Layer: "contract", NodeType: "endpoint", DomainKey: "d",
		Name: "GET /orders", Status: "active", Confidence: 0.9, Freshness: 1, TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertEdge(store.EdgeRow{EdgeKey: mkShopCtl + "->" + mkEndpoint + ":EXPOSES_ENDPOINT", FromNodeID: ctl.NodeID,
		ToNodeID: epID, EdgeType: "EXPOSES_ENDPOINT", DerivationKind: "hard", Active: true, Confidence: 0.9, Freshness: 1,
		TrustScore: 0.9, Metadata: "{}", LastSeenRevisionID: rev}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestNormalizeMarks(t *testing.T) {
	got, err := NormalizeMarks(map[string]Mark{" k ": {Kind: " Breaks ", Note: "line1\nline2\t<b>‮!"}})
	if err != nil {
		t.Fatal(err)
	}
	if m := got["k"]; m.Kind != "breaks" || m.Note != "line1 line2 <b>!" {
		t.Fatalf("normalized = %+v", got)
	}
	if got, err := NormalizeMarks(nil); got != nil || err != nil {
		t.Fatalf("empty marks = %v, %v", got, err)
	}
	if _, err := NormalizeMarks(map[string]Mark{"k": {Kind: "explode"}}); err == nil || !strings.Contains(err.Error(), "breaks|changed|focus|note") {
		t.Fatalf("unknown kind error = %v", err)
	}
	many := map[string]Mark{}
	for i := 0; i <= MaxMarks; i++ {
		many[fmt.Sprintf("k%d", i)] = Mark{Kind: "note"}
	}
	if _, err := NormalizeMarks(many); err == nil || !strings.Contains(err.Error(), "at most 50") {
		t.Fatalf("51 marks error = %v", err)
	}
	cyr := strings.Repeat("ж", MaxMarkNote)
	if _, err := NormalizeMarks(map[string]Mark{"k": {Kind: "note", Note: cyr}}); err != nil {
		t.Fatalf("200 Cyrillic characters must pass (characters, not bytes): %v", err)
	}
	if _, err := NormalizeMarks(map[string]Mark{"k": {Kind: "note", Note: cyr + "ж"}}); err == nil {
		t.Fatal("201 characters must be refused")
	}
	// Two distinct keys that trim to the same string must error, not
	// silently pick one by map iteration order (which Go randomizes).
	if _, err := NormalizeMarks(map[string]Mark{"k": {Kind: "note"}, "k ": {Kind: "breaks"}}); err == nil || !strings.Contains(err.Error(), "same key") {
		t.Fatalf("post-trim key collision error = %v", err)
	}
}

func TestPlaceMarks_C2(t *testing.T) {
	st := synthStore(t)
	spec := PresetSpec("c2", "d", "")
	spec.Marks = map[string]Mark{
		mkShopProv:             {Kind: "changed", Note: "rename publish"},
		mkTopic:                {Kind: "breaks"},
		mkKafka:                {Kind: "focus"},
		mkRedis:                {Kind: "note", Note: "declared only"},
		mkCall:                 {Kind: "breaks", Note: "timeout"},
		mkInjects:              {Kind: "note"},
		"code:provider:d:nope": {Kind: "note"},
	}
	v, err := BuildView(st, spec)
	if err != nil {
		t.Fatal(err)
	}
	group := func(key string) *VGroup {
		for i := range v.Groups {
			if v.Groups[i].Key == key {
				return &v.Groups[i]
			}
		}
		return nil
	}
	node := func(key string) *VNode {
		for i := range v.Nodes {
			if v.Nodes[i].Key == key {
				return &v.Nodes[i]
			}
		}
		return nil
	}
	if g := group(mkShopSvc); g == nil || len(g.Marks) != 1 || g.Marks[0].Key != mkShopProv || g.Marks[0].Name != "shop.service" || g.Marks[0].Kind != "changed" {
		t.Errorf("shop.service folds into the shop-api card: %+v", g)
	}
	if n := node(mkTopic); n == nil || len(n.Marks) != 1 || n.Marks[0].Kind != "breaks" {
		t.Errorf("the topic card carries its own mark: %+v", n)
	}
	if g := group(mkKafka); g == nil || len(g.Marks) != 1 || g.Marks[0].Key != mkKafka {
		t.Errorf("the broker frame carries its own mark: %+v", g)
	}
	r := node(mkRedis)
	if r == nil || r.RenderMode != "box" || r.SalienceReason != "mark" || len(r.Marks) != 1 {
		t.Errorf("isolated redis is promoted by its mark: %+v", r)
	}
	for _, h := range v.Hidden {
		if h.Type == "cache" {
			t.Errorf("promoted redis is still counted as hidden: %+v", v.Hidden)
		}
	}
	var call *VEdge
	for i := range v.Edges {
		if v.Edges[i].From == mkShopSvc && v.Edges[i].To == mkMailSvc {
			call = &v.Edges[i]
		}
	}
	if call == nil || len(call.Marks) != 1 || call.Marks[0].Key != mkCall || call.Marks[0].Name != "shop.service → mail-api" {
		t.Errorf("the component call's mark sits on the lifted arrow: %+v", call)
	}
	if strings.Join(v.Unplaced, ",") != mkInjects {
		t.Errorf("unplaced = %v, want the intra-card INJECTS edge", v.Unplaced)
	}
	if !markTestHas(v.Missing, "code:provider:d:nope") {
		t.Errorf("missing = %v", v.Missing)
	}
	if len(v.Spec.Marks) != 7 || v.Spec.Marks[mkRedis].Note != "declared only" {
		t.Errorf("the view echoes the normalized marks: %+v", v.Spec.Marks)
	}
	if got := len(v.PlacedMarks()); got != 5 {
		t.Errorf("placed = %d marks: %+v", got, v.PlacedMarks())
	}
}

func TestPlaceMarks_EndpointPillLandsOnItsExposer(t *testing.T) {
	st := synthWithEndpoint(t)
	spec := PresetSpec("c3", "d", "shop-api")
	spec.Marks = map[string]Mark{mkEndpoint: {Kind: "breaks", Note: "404 after deploy"}}
	v, err := BuildView(st, spec)
	if err != nil {
		t.Fatal(err)
	}
	var ctl *VNode
	for i := range v.Nodes {
		if v.Nodes[i].Key == mkShopCtl {
			ctl = &v.Nodes[i]
		}
	}
	if ctl == nil || len(ctl.Marks) != 1 || ctl.Marks[0].Key != mkEndpoint || ctl.Marks[0].Name != "GET /orders" {
		t.Fatalf("the endpoint's mark sits on its exposer: %+v", ctl)
	}
	if len(ctl.Endpoints) != 1 {
		t.Fatalf("the endpoint stays a pill: %+v", ctl.Endpoints)
	}
}

func viewMembership(v *View) string {
	var parts []string
	for _, n := range v.Nodes {
		parts = append(parts, "n:"+n.Key)
	}
	for _, g := range v.Groups {
		parts = append(parts, "g:"+g.Key)
	}
	for _, e := range v.Edges {
		parts = append(parts, "e:"+e.From+">"+e.To+":"+e.Kind)
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

// Marks never change view membership: every preset, every node marked.
func TestPlaceMarks_NeverChangeMembership(t *testing.T) {
	st := synthWithEndpoint(t)
	all, err := st.ListNodes(store.NodeFilter{Domain: "d"})
	if err != nil {
		t.Fatal(err)
	}
	marks := map[string]Mark{}
	for _, n := range all {
		marks[n.NodeKey] = Mark{Kind: "note"}
	}
	for _, c := range []struct{ preset, target string }{{"c1", ""}, {"c2", ""}, {"c3", "shop-api"}, {"c3", ""}, {"api", ""}, {"data", ""}, {"flows", ""}} {
		plain, err := BuildView(st, PresetSpec(c.preset, "d", c.target))
		if err != nil {
			t.Fatalf("%s: %v", c.preset, err)
		}
		spec := PresetSpec(c.preset, "d", c.target)
		spec.Marks = marks
		marked, err := BuildView(st, spec)
		if err != nil {
			t.Fatalf("%s marked: %v", c.preset, err)
		}
		if a, b := viewMembership(plain), viewMembership(marked); a != b {
			t.Errorf("%s %s: marks changed membership\nplain:  %s\nmarked: %s", c.preset, c.target, a, b)
		}
	}
}
