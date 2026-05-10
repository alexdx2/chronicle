package rules

// Built-in framework rulesets.
// These are loaded based on the manifest's tech/frameworks list.

// NestJS decorator rules.
var NestJS = Ruleset{
	Name: "nestjs",
	Decorators: map[string]DecoratorRule{
		"Controller":     {SetsFromType: "controller", CapturesPrefix: true},
		"Module":         {SetsFromType: "module"},
		"Injectable":     {SetsFromType: "provider"},
		"Get":            {EmitsKind: "endpoint", EmitsMethod: "GET", TargetFrom: "first_string_arg"},
		"Post":           {EmitsKind: "endpoint", EmitsMethod: "POST", TargetFrom: "first_string_arg"},
		"Put":            {EmitsKind: "endpoint", EmitsMethod: "PUT", TargetFrom: "first_string_arg"},
		"Delete":         {EmitsKind: "endpoint", EmitsMethod: "DELETE", TargetFrom: "first_string_arg"},
		"Patch":          {EmitsKind: "endpoint", EmitsMethod: "PATCH", TargetFrom: "first_string_arg"},
		"SubscribeMessage": {EmitsKind: "endpoint", EmitsMethod: "WS", TargetFrom: "first_string_arg"},
		"EventPattern":   {EmitsKind: "consumes", TargetFrom: "first_string_arg"},
		"OnEvent":        {EmitsKind: "consumes", TargetFrom: "first_string_arg"},
		"Process":        {EmitsKind: "consumes", TargetFrom: "first_string_arg"},
	},
}

// GraphQL decorator rules (NestJS GraphQL or Apollo).
var GraphQL = Ruleset{
	Name: "graphql",
	Decorators: map[string]DecoratorRule{
		"Resolver":     {SetsFromType: "controller"},
		"Query":        {EmitsKind: "endpoint", EmitsMethod: "Query", TargetFrom: "method_name_if_no_string_arg"},
		"Mutation":     {EmitsKind: "endpoint", EmitsMethod: "Mutation", TargetFrom: "method_name_if_no_string_arg"},
		"Subscription": {EmitsKind: "endpoint", EmitsMethod: "Subscription", TargetFrom: "method_name_if_no_string_arg"},
		"ResolveField": {EmitsKind: "endpoint", EmitsMethod: "ResolveField", TargetFrom: "method_name_if_no_string_arg"},
	},
}

// WebSocket gateway rules.
var WebSocket = Ruleset{
	Name: "websocket",
	Decorators: map[string]DecoratorRule{
		"WebSocketGateway": {SetsFromType: "controller"},
	},
}

// DefaultRulesets returns NestJS + GraphQL + WebSocket (common TypeScript stack).
func DefaultRulesets() []Ruleset {
	return []Ruleset{NestJS, GraphQL, WebSocket}
}

// RulesetsForTech returns rulesets matching the tech list from the manifest.
func RulesetsForTech(tech []string) []Ruleset {
	var result []Ruleset
	techSet := make(map[string]bool)
	for _, t := range tech {
		techSet[t] = true
	}

	if techSet["nestjs"] || techSet["nest"] {
		result = append(result, NestJS)
	}
	if techSet["graphql"] || techSet["apollo"] {
		result = append(result, GraphQL)
	}
	if techSet["socketio"] || techSet["websocket"] || techSet["socket.io"] {
		result = append(result, WebSocket)
	}

	// If nothing matched, return all defaults
	if len(result) == 0 {
		return DefaultRulesets()
	}
	return result
}
