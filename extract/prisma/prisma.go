package prisma

import (
	"regexp"
	"strings"
)

type Model struct {
	Name   string
	Line   int
	Fields []Field
}

type Field struct {
	Name    string
	Type    string
	IsArray bool
}

type Enum struct {
	Name string
	Line int
}

type Relation struct {
	From      string
	To        string
	FieldName string
}

type Result struct {
	Models    []Model
	Enums     []Enum
	Relations []Relation
}

var (
	modelRe = regexp.MustCompile(`(?m)^model\s+(\w+)\s*\{`)
	enumRe  = regexp.MustCompile(`(?m)^enum\s+(\w+)\s*\{`)
	fieldRe = regexp.MustCompile(`^\s+(\w+)\s+(\w+)(\[\])?`)
)

func Extract(content []byte) Result {
	text := string(content)
	var result Result
	modelNames := map[string]bool{}

	// Extract models with their fields
	for _, match := range modelRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		line := strings.Count(text[:match[0]], "\n") + 1

		model := Model{Name: name, Line: line}

		// Find the matching closing brace
		braceStart := strings.Index(text[match[0]:], "{")
		if braceStart < 0 {
			continue
		}
		blockStart := match[0] + braceStart + 1
		depth := 1
		pos := blockStart
		for pos < len(text) && depth > 0 {
			if text[pos] == '{' {
				depth++
			} else if text[pos] == '}' {
				depth--
			}
			pos++
		}
		block := text[blockStart : pos-1]

		// Parse fields
		for _, fieldLine := range strings.Split(block, "\n") {
			trimmed := strings.TrimSpace(fieldLine)
			if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "@@") {
				continue
			}
			if m := fieldRe.FindStringSubmatch(fieldLine); m != nil {
				fieldName := m[1]
				fieldType := m[2]
				isArray := m[3] == "[]"

				if isBuiltinType(fieldType) {
					continue
				}

				model.Fields = append(model.Fields, Field{
					Name:    fieldName,
					Type:    fieldType,
					IsArray: isArray,
				})
			}
		}

		result.Models = append(result.Models, model)
		modelNames[name] = true
	}

	// Extract enums
	for _, match := range enumRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[match[2]:match[3]]
		line := strings.Count(text[:match[0]], "\n") + 1
		result.Enums = append(result.Enums, Enum{Name: name, Line: line})
	}

	// Detect relations: fields whose type matches a known model or enum name
	for _, model := range result.Models {
		for _, field := range model.Fields {
			if modelNames[field.Type] {
				result.Relations = append(result.Relations, Relation{
					From:      model.Name,
					To:        field.Type,
					FieldName: field.Name,
				})
			}
		}
	}

	return result
}

func isBuiltinType(t string) bool {
	switch t {
	case "String", "Int", "Float", "Boolean", "DateTime", "Json", "Bytes", "BigInt", "Decimal":
		return true
	}
	return false
}
