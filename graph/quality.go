// graph/quality.go
package graph

import (
	"fmt"
	"strings"
)

// QualityWarning represents a graph quality issue found after scan.
type QualityWarning struct {
	Category string `json:"category"` // "endpoint", "service", "data", "noise", "topic"
	Severity string `json:"severity"` // "critical", "warning", "info"
	Message  string `json:"message"`
	NodeKey  string `json:"node_key,omitempty"`
}

// CheckServiceNodes verifies that expected service nodes exist in the graph.
func CheckServiceNodes(expectedServices []string, actualNodeKeys []string) []QualityWarning {
	actual := map[string]bool{}
	for _, k := range actualNodeKeys {
		parts := strings.SplitN(k, ":", 4)
		if len(parts) >= 4 {
			actual[parts[3]] = true
		}
	}
	var warnings []QualityWarning
	for _, svc := range expectedServices {
		if !actual[svc] {
			warnings = append(warnings, QualityWarning{
				Category: "service",
				Severity: "critical",
				Message:  fmt.Sprintf("expected service node for '%s' but none found", svc),
			})
		}
	}
	return warnings
}

// CheckDataModels verifies that Prisma models exist if Prisma files were scanned.
func CheckDataModels(hasPrismaFiles bool, modelNodeCount int) []QualityWarning {
	if hasPrismaFiles && modelNodeCount == 0 {
		return []QualityWarning{{
			Category: "data",
			Severity: "critical",
			Message:  "Prisma schema files found but no model/enum nodes in graph",
		}}
	}
	return nil
}

// CheckNoiseRatio warns if support providers outnumber core providers.
func CheckNoiseRatio(totalProviders, supportProviders int) []QualityWarning {
	if totalProviders > 0 && supportProviders*2 > totalProviders {
		return []QualityWarning{{
			Category: "noise",
			Severity: "warning",
			Message:  fmt.Sprintf("high noise ratio: %d/%d providers are support components", supportProviders, totalProviders),
		}}
	}
	return nil
}

// CheckEndpointPrefixes verifies endpoints include controller prefix when one was detected.
func CheckEndpointPrefixes(endpoints []EndpointInfo) []QualityWarning {
	var warnings []QualityWarning
	for _, ep := range endpoints {
		if ep.ControllerPrefix == "" {
			continue
		}
		prefix := strings.Trim(ep.ControllerPrefix, "/")
		path := strings.TrimPrefix(ep.Path, "/")
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			warnings = append(warnings, QualityWarning{
				Category: "endpoint",
				Severity: "critical",
				Message:  fmt.Sprintf("endpoint %s has controller prefix '%s' but path /%s doesn't include it", ep.NodeKey, prefix, path),
				NodeKey:  ep.NodeKey,
			})
		}
	}
	return warnings
}

// EndpointInfo holds endpoint data for quality checking.
type EndpointInfo struct {
	NodeKey          string
	Path             string
	ControllerPrefix string
}
