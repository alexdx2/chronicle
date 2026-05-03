package verify

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// PrismaSchemaVerifier checks Prisma schema files for models, fields, relations.
// This is a conservative lexical parser, not a full Prisma parser.
type PrismaSchemaVerifier struct{}

func (v *PrismaSchemaVerifier) Kind() string { return "prisma_model" }

// PrismaAssertion describes an expected Prisma model/field.
type PrismaAssertion struct {
	Model      string `json:"model"`                 // e.g. "Order"
	HasField   string `json:"has_field,omitempty"`    // e.g. "userId"
	RelationTo string `json:"relation_to,omitempty"` // e.g. "User"
}

var modelBlockRegex = regexp.MustCompile(`(?m)^model\s+(\w+)\s*\{`)

func (v *PrismaSchemaVerifier) Verify(fileContent []byte, assertion json.RawMessage, oldLocator *Locator) (*Result, error) {
	var a PrismaAssertion
	if err := json.Unmarshal(assertion, &a); err != nil {
		return nil, fmt.Errorf("invalid prisma_model assertion: %w", err)
	}
	if a.Model == "" {
		return nil, fmt.Errorf("prisma_model assertion missing 'model' field")
	}

	content := string(fileContent)
	lines := strings.Split(content, "\n")

	// Find model block
	modelStart, modelEnd := findPrismaModelBlock(lines, a.Model)
	if modelStart < 0 {
		return &Result{
			Status:          "missing",
			Confidence:      0.90,
			Reason:          fmt.Sprintf("model %q not found in schema", a.Model),
			SuggestedAction: "mark_stale",
		}, nil
	}

	// Model found — check field if specified
	if a.HasField != "" {
		fieldLine := findPrismaField(lines, modelStart, modelEnd, a.HasField)
		if fieldLine < 0 {
			return &Result{
				Status:          "valid",
				ChangeType:      "value_changed",
				NewLocator:      &Locator{LineStart: modelStart + 1, LineEnd: modelEnd + 1},
				Confidence:      0.80,
				Reason:          fmt.Sprintf("model %s exists but field %q not found", a.Model, a.HasField),
				SuggestedAction: "needs_claude",
			}, nil
		}

		// Check relation if specified
		if a.RelationTo != "" {
			line := lines[fieldLine]
			if !strings.Contains(line, a.RelationTo) {
				return &Result{
					Status:          "valid",
					ChangeType:      "value_changed",
					NewLocator:      &Locator{LineStart: fieldLine + 1, LineEnd: fieldLine + 1},
					Confidence:      0.75,
					Reason:          fmt.Sprintf("field %s exists but relation to %q not found on that line", a.HasField, a.RelationTo),
					SuggestedAction: "needs_claude",
				}, nil
			}
		}

		return &Result{
			Status:          "valid",
			NewLocator:      &Locator{LineStart: fieldLine + 1, LineEnd: fieldLine + 1},
			Confidence:      0.90,
			SuggestedAction: "revalidate",
		}, nil
	}

	// Just checking model exists
	return &Result{
		Status:          "valid",
		NewLocator:      &Locator{LineStart: modelStart + 1, LineEnd: modelEnd + 1},
		Confidence:      0.90,
		SuggestedAction: "revalidate",
	}, nil
}

// findPrismaModelBlock finds the start and end line indices of a model block.
func findPrismaModelBlock(lines []string, modelName string) (int, int) {
	prefix := "model " + modelName
	braceDepth := 0
	startLine := -1

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if startLine < 0 {
			if strings.HasPrefix(trimmed, prefix) && strings.Contains(trimmed, "{") {
				startLine = i
				braceDepth = 1
				continue
			}
		} else {
			braceDepth += strings.Count(trimmed, "{") - strings.Count(trimmed, "}")
			if braceDepth <= 0 {
				return startLine, i
			}
		}
	}

	if startLine >= 0 {
		return startLine, len(lines) - 1
	}
	return -1, -1
}

// findPrismaField finds a field line within a model block.
func findPrismaField(lines []string, modelStart, modelEnd int, fieldName string) int {
	for i := modelStart + 1; i <= modelEnd && i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || trimmed == "}" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "@@") {
			continue
		}
		// Field lines look like: "fieldName Type @relation(...)"
		parts := strings.Fields(trimmed)
		if len(parts) >= 1 && parts[0] == fieldName {
			return i
		}
	}
	return -1
}
