package e2e

import (
	"testing"

	"github.com/alexdx2/chronicle-core/store"
	"github.com/alexdx2/chronicle-core/validate"
)

// TestGraphCompleteness_EndpointsLinkedToControllers verifies every endpoint
// has at least one EXPOSES_ENDPOINT edge FROM a controller.
func TestGraphCompleteness_EndpointsLinkedToControllers(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	endpoints, _ := g.Store().ListNodes(store.NodeFilter{Layer: "contract", NodeType: "endpoint", Domain: "tomandjerry"})
	if len(endpoints) == 0 {
		t.Fatal("no endpoints found")
	}

	for _, ep := range endpoints {
		reverseDeps, err := g.QueryReverseDeps(ep.NodeKey, 1, nil)
		if err != nil {
			t.Fatalf("QueryReverseDeps(%s): %v", ep.NodeKey, err)
		}
		foundController := false
		for _, rd := range reverseDeps {
			if rd.NodeType == "controller" {
				foundController = true
				break
			}
		}
		if !foundController {
			t.Errorf("endpoint %s has no controller exposing it (EXPOSES_ENDPOINT)", ep.NodeKey)
		}
	}
}

// TestGraphCompleteness_ControllersInjectServices verifies every controller
// injects at least one provider/service.
func TestGraphCompleteness_ControllersInjectServices(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	controllers, _ := g.Store().ListNodes(store.NodeFilter{Layer: "code", NodeType: "controller", Domain: "tomandjerry"})
	if len(controllers) != 4 {
		t.Fatalf("controllers = %d, want 4", len(controllers))
	}

	expected := map[string]string{
		"code:controller:tomandjerry:arenacontroller": "code:provider:tomandjerry:arenaservice",
		"code:controller:tomandjerry:tomcontroller":   "code:provider:tomandjerry:tomservice",
		"code:controller:tomandjerry:jerrycontroller": "code:provider:tomandjerry:jerryservice",
		"code:controller:tomandjerry:statscontroller": "code:provider:tomandjerry:spectatorservice",
	}

	for ctrlKey, svcKey := range expected {
		deps, err := g.QueryDeps(ctrlKey, 1, nil)
		if err != nil {
			t.Fatalf("QueryDeps(%s): %v", ctrlKey, err)
		}
		found := false
		for _, d := range deps {
			if d.NodeKey == svcKey {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s should INJECT %s", ctrlKey, svcKey)
		}
	}
}

// TestGraphCompleteness_CrossServiceCalls verifies TomClient and JerryClient
// have CALLS_SERVICE edges to tom-api and jerry-api.
func TestGraphCompleteness_CrossServiceCalls(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	cases := []struct {
		client  string
		service string
	}{
		{"code:provider:tomandjerry:tomclient", "service:service:tomandjerry:tom-api"},
		{"code:provider:tomandjerry:jerryclient", "service:service:tomandjerry:jerry-api"},
	}

	for _, tc := range cases {
		deps, err := g.QueryDeps(tc.client, 1, nil)
		if err != nil {
			t.Fatalf("QueryDeps(%s): %v", tc.client, err)
		}
		found := false
		for _, d := range deps {
			if d.NodeKey == tc.service {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s should CALLS_SERVICE %s", tc.client, tc.service)
		}
	}
}

// TestGraphCompleteness_KafkaFlow verifies both PUBLISHES_TOPIC and
// CONSUMES_TOPIC edges exist for the battle-results topic.
func TestGraphCompleteness_KafkaFlow(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	topicKey := "contract:topic:tomandjerry:battle-results"

	// BattleResultProducer PUBLISHES_TOPIC battle-results
	producerDeps, err := g.QueryDeps("code:provider:tomandjerry:battleresultproducer", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps producer: %v", err)
	}
	foundPublish := false
	for _, d := range producerDeps {
		if d.NodeKey == topicKey {
			foundPublish = true
			break
		}
	}
	if !foundPublish {
		t.Error("BattleResultProducer should PUBLISHES_TOPIC battle-results")
	}

	// BattleResultConsumer CONSUMES_TOPIC battle-results
	consumerDeps, err := g.QueryDeps("code:provider:tomandjerry:battleresultconsumer", 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps consumer: %v", err)
	}
	foundConsume := false
	for _, d := range consumerDeps {
		if d.NodeKey == topicKey {
			foundConsume = true
			break
		}
	}
	if !foundConsume {
		t.Error("BattleResultConsumer should CONSUMES_TOPIC battle-results")
	}
}

// TestGraphCompleteness_DataModelUsage verifies services use the correct models.
func TestGraphCompleteness_DataModelUsage(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	cases := []struct {
		service string
		models  []string
	}{
		{
			"code:provider:tomandjerry:tomservice",
			[]string{"data:model:tomandjerry:cat", "data:model:tomandjerry:catweapon"},
		},
		{
			"code:provider:tomandjerry:jerryservice",
			[]string{"data:model:tomandjerry:mouse"},
		},
		{
			"code:provider:tomandjerry:arenaservice",
			[]string{"data:model:tomandjerry:battleevent"},
		},
	}

	for _, tc := range cases {
		deps, err := g.QueryDeps(tc.service, 1, nil)
		if err != nil {
			t.Fatalf("QueryDeps(%s): %v", tc.service, err)
		}
		depSet := make(map[string]bool)
		for _, d := range deps {
			depSet[d.NodeKey] = true
		}
		for _, model := range tc.models {
			if !depSet[model] {
				t.Errorf("%s should USES_MODEL %s", tc.service, model)
			}
		}
	}
}

// TestGraphCompleteness_ModuleContainment verifies each module CONTAINS its
// expected controllers and providers.
func TestGraphCompleteness_ModuleContainment(t *testing.T) {
	g, _ := setupTomAndJerry(t)

	cases := []struct {
		module   string
		contains []string
	}{
		{
			"code:module:tomandjerry:arenamodule",
			[]string{
				"code:controller:tomandjerry:arenacontroller",
				"code:provider:tomandjerry:arenaservice",
				"code:provider:tomandjerry:tomclient",
				"code:provider:tomandjerry:jerryclient",
				"code:provider:tomandjerry:battleresultproducer",
				"code:provider:tomandjerry:prismaservice-arena",
			},
		},
		{
			"code:module:tomandjerry:tommodule",
			[]string{
				"code:controller:tomandjerry:tomcontroller",
				"code:provider:tomandjerry:tomservice",
				"code:provider:tomandjerry:prismaservice-tom",
			},
		},
		{
			"code:module:tomandjerry:jerrymodule",
			[]string{
				"code:controller:tomandjerry:jerrycontroller",
				"code:provider:tomandjerry:jerryservice",
				"code:provider:tomandjerry:prismaservice-jerry",
			},
		},
		{
			"code:module:tomandjerry:spectatorsmodule",
			[]string{
				"code:controller:tomandjerry:statscontroller",
				"code:provider:tomandjerry:spectatorservice",
				"code:provider:tomandjerry:battleresultconsumer",
			},
		},
	}

	for _, tc := range cases {
		deps, err := g.QueryDeps(tc.module, 1, nil)
		if err != nil {
			t.Fatalf("QueryDeps(%s): %v", tc.module, err)
		}
		depSet := make(map[string]bool)
		for _, d := range deps {
			depSet[d.NodeKey] = true
		}
		for _, child := range tc.contains {
			if !depSet[child] {
				t.Errorf("%s should CONTAINS %s", tc.module, child)
			}
		}
	}
}

// TestGraphCompleteness_InfraFromManifest verifies that infrastructure nodes
// can be added separately and linked to topics via BELONGS_TO.
func TestGraphCompleteness_InfraFromManifest(t *testing.T) {
	g, _ := setupTomAndJerry(t)
	s := g.Store()

	revID, _ := s.CreateRevision("tomandjerry", "abc123", "def456", "manual", "full", "{}")

	// Add infra node for Kafka broker (using "infrastructure" node type from registry)
	kafkaKey := "infra:infrastructure:tomandjerry:kafka:9092"
	g.UpsertNode(validate.NodeInput{
		NodeKey:   kafkaKey,
		Layer:     "infra",
		NodeType:  "infrastructure",
		DomainKey: "tomandjerry",
		Name:      "kafka:9092",
	}, revID)

	// Add BELONGS_TO edge from topic to broker
	topicKey := "contract:topic:tomandjerry:battle-results"
	edgeKey := topicKey + "->" + kafkaKey + ":BELONGS_TO"
	topicNode, _ := s.GetNodeByKey(topicKey)
	kafkaNode, _ := s.GetNodeByKey(kafkaKey)
	if topicNode == nil {
		t.Fatal("topic node not found after import")
	}
	if kafkaNode == nil {
		t.Fatal("kafka infra node not found after upsert")
	}

	_, err := s.UpsertEdge(store.EdgeRow{
		EdgeKey:        edgeKey,
		FromNodeID:     topicNode.NodeID,
		ToNodeID:       kafkaNode.NodeID,
		EdgeType:       "BELONGS_TO",
		DerivationKind: "linked",
		Active:         true,
		FromNodeKey:    topicKey,
		ToNodeKey:      kafkaKey,
	})
	if err != nil {
		t.Fatalf("UpsertEdge BELONGS_TO: %v", err)
	}

	// Verify infra node exists
	node, err := s.GetNodeByKey(kafkaKey)
	if err != nil || node == nil {
		t.Fatal("infra:broker:kafka:9092 node should exist")
	}
	if node.Layer != "infra" {
		t.Errorf("infra node layer = %q, want infra", node.Layer)
	}
	if node.NodeType != "infrastructure" {
		t.Errorf("infra node type = %q, want infrastructure", node.NodeType)
	}

	// Verify BELONGS_TO edge from topic to broker
	deps, err := g.QueryDeps(topicKey, 1, nil)
	if err != nil {
		t.Fatalf("QueryDeps topic: %v", err)
	}
	foundBroker := false
	for _, d := range deps {
		if d.NodeKey == kafkaKey {
			foundBroker = true
			break
		}
	}
	if !foundBroker {
		t.Error("battle-results topic should BELONGS_TO kafka:9092 infrastructure node")
	}
}
