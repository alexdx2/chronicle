package rules

// Built-in framework rulesets.
// These are loaded based on the manifest's tech/frameworks list.

// NestJS decorator rules.
var NestJS = Ruleset{
	Name: "nestjs",
	Decorators: map[string]DecoratorRule{
		"Controller":       {SetsFromType: "controller", CapturesPrefix: true},
		"Module":           {SetsFromType: "module"},
		"Injectable":       {SetsFromType: "provider"},
		"Get":              {EmitsKind: "endpoint", EmitsMethod: "GET", TargetFrom: "first_string_arg"},
		"Post":             {EmitsKind: "endpoint", EmitsMethod: "POST", TargetFrom: "first_string_arg"},
		"Put":              {EmitsKind: "endpoint", EmitsMethod: "PUT", TargetFrom: "first_string_arg"},
		"Delete":           {EmitsKind: "endpoint", EmitsMethod: "DELETE", TargetFrom: "first_string_arg"},
		"Patch":            {EmitsKind: "endpoint", EmitsMethod: "PATCH", TargetFrom: "first_string_arg"},
		"SubscribeMessage": {EmitsKind: "endpoint", EmitsMethod: "WS", TargetFrom: "first_string_arg"},
		// @OnEvent (EventEmitter2) is in-process: transport=local suppresses broker topic nodes.
		// @EventPattern (@nestjs/microservices) is a Kafka/broker consumer: no local tag,
		// so the resolver creates a real CONSUMES_TOPIC edge to the broker topic node.
		"EventPattern": {EmitsKind: "consumes", TargetFrom: "first_string_arg"},
		"OnEvent":      {EmitsKind: "consumes", TargetFrom: "first_string_arg", TransportTag: "local"},
		// @Process('jobname') — job-name handlers are NOT separate topics; skip emission.
		// (Handled by the Bull ruleset instead via @Processor on the class.)
	},
}

// Bull queue ruleset (@nestjs/bull / bullmq patterns).
// @Processor(X) on a class → consumes X (the queue name), transport=queue.
// @InjectQueue(X) in a constructor param → produces X (the queue name), transport=queue.
// @Process('jobname') on a method → no separate topic emitted (job names ≠ topics).
// X may be a string literal or a module-level const identifier — resolved via StringConsts.
var Bull = Ruleset{
	Name: "bull",
	Decorators: map[string]DecoratorRule{
		"Processor":   {EmitsKind: "consumes", TargetFrom: "first_string_arg", TransportTag: "queue", ResolveIdent: true},
		"InjectQueue": {EmitsKind: "produces", TargetFrom: "first_string_arg", TransportTag: "queue", ResolveIdent: true},
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
// @WebSocketGateway — a NestJS provider registered in the providers[] array of a module,
// not a controller. Only @Controller (REST/GraphQL) defines controllers.
// Gateways are typed as "provider" so they land in the right module slot and
// do not get a phantom EXPOSES_ENDPOINT duplicate when the module CONTAINS edge resolves.
var WebSocket = Ruleset{
	Name: "websocket",
	Decorators: map[string]DecoratorRule{
		"WebSocketGateway": {SetsFromType: "provider"},
	},
}

// DefaultRulesets returns NestJS + GraphQL + WebSocket + Bull (common TypeScript stack).
// Bull decorators (@InjectQueue, @Processor) are unambiguous — false positives are impossible
// without the @nestjs/bull imports, so it is safe to include in the default set.
func DefaultRulesets() []Ruleset {
	return []Ruleset{NestJS, GraphQL, WebSocket, Bull}
}

// RulesetsForTech returns rulesets matching the tech list from the manifest.
func RulesetsForTech(tech []string) []Ruleset {
	var result []Ruleset
	techSet := make(map[string]bool)
	for _, t := range tech {
		techSet[t] = true
	}

	if techSet["nestjs"] || techSet["nest"] {
		// Bull is a NestJS integration and its decorators are unambiguous —
		// projects rarely list "bull" in tech explicitly.
		result = append(result, NestJS, Bull, WebSocket)
	}
	if techSet["graphql"] || techSet["apollo"] {
		result = append(result, GraphQL)
	}
	if techSet["socketio"] || techSet["websocket"] || techSet["socket.io"] {
		result = append(result, WebSocket)
	}
	if techSet["bull"] || techSet["bullmq"] {
		result = append(result, Bull)
	}

	// If nothing matched, return all defaults (includes Bull — decorators are unambiguous)
	if len(result) == 0 {
		return DefaultRulesets()
	}
	return result
}
