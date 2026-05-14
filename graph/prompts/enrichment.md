# ENRICHMENT: CANDIDATE CLASSIFICATION + MISSED FACT EXTRACTION

The deterministic extractor already produced `ast_facts` and `candidates`.

Your job:
1. Classify every candidate
2. Add architectural facts that deterministic extraction cannot see

Output MUST be a valid JSON object:

```json
{
  "candidate_decisions": [],
  "additional_facts": []
}
```

Do not output explanations. Do not output a plain array.

# Part 1: classify candidates

For every item in `candidates`, output exactly one decision in `candidate_decisions`.

## Accept

```json
{
  "candidate_id": "COPY_ID",
  "decision": "accept",
  "fact": {"kind":"calls_service","to":"OrderService","method":"create"}
}
```

## Reject

```json
{
  "candidate_id": "COPY_ID",
  "decision": "reject",
  "reason": "generic_utility"
}
```

`candidate_id` MUST be copied exactly from the candidate's `id` field.

## Allowed fact kinds for candidates

```json
{"kind":"calls_service","to":"ServiceName","method":"methodName"}
{"kind":"uses_model","to":"ModelName"}
{"kind":"produces","to":"event.name","method":"methodName"}
{"kind":"consumes","to":"event.name","method":"methodName"}
{"kind":"http_call","method":"POST","target":"https://example.com/path"}
{"kind":"calls_endpoint","method":"GET","target":"/api/path"}
```

## How to decide

Use the candidate fields in this priority:

1. If `resolved_type` exists: use it as `to` value (e.g., resolved_type="OrderService" -> to="OrderService")
2. If `is_architectural` is true: accept as `calls_service`
3. If `receiver` contains "prisma" or ORM client and method accesses a table: accept as `uses_model`
4. If receiver_origin="constructor_param": likely architectural, accept
5. If none of the above: check technology instructions for mapping rules
6. If still unclear: reject

## Reject reasons

Use one of: `generic_utility`, `local_helper`, `language_builtin`, `logging`, `not_architectural`, `unresolved_receiver`, `test_assertion`, `framework_lifecycle`

## Reject these

- logging (console.log, logger.info)
- array/string/date operations
- validation/mapping/transformation helpers
- local private helpers with no architectural boundary
- test assertions
- framework lifecycle methods without business meaning

# Part 2: additional facts

After classifying candidates, check for architectural facts NOT in candidates or ast_facts.

Output them in `additional_facts`:

```json
{"kind":"declares_service","to":"service-name"}
{"kind":"http_call","method":"POST","target":"https://api.stripe.com/v1/charges"}
{"kind":"produces","to":"order.created","method":"publishEvent"}
{"kind":"consumes","to":"order.created","method":"handleEvent"}
{"kind":"model","to":"ModelName"}
{"kind":"enum","to":"EnumName"}
{"kind":"model_relation","from":"A","to":"B"}
{"kind":"calls_endpoint","method":"GET","target":"/api/users"}
{"kind":"calls_service","to":"ServiceName"}
{"kind":"uses_model","to":"User"}
```

Do NOT re-emit facts already in `ast_facts`.
Do NOT re-emit facts already accepted from candidates.
Do NOT invent candidates or kind values.

## File-type obligations (CRITICAL)

These checks are NOT optional. You MUST verify them for every file.

**Package/build manifest files** (package.json, go.mod, pyproject.toml, pom.xml):
- MUST check: does this file confirm a deployable service boundary?
- IF YES: emit `{"kind":"declares_service","to":"<package-name>"}`

**Schema/contract files** (*.prisma, *.graphql, *.proto, openapi.yaml):
- MUST check: all model definitions, enum definitions, model relations
- EMIT: `model`, `enum`, `model_relation` for EVERY definition

**Module/registration files** (@Module, providers arrays, DI containers):
- MUST check: every controller and provider registered
- EMIT: one `provides` fact per entry in controllers/providers. Set `from_type: "module"`.
- MISSING provides = MISSING CONTAINS edges = broken graph

**Controller/handler files**:
- MUST check: every route/endpoint/handler method
- EMIT: `endpoint` for each. `injects` for each DI dependency.

**Service/provider files**:
- MUST check: DI deps, model usage, cross-service calls, event pub/sub
- EMIT: `injects`, `uses_model`, `calls_service`, `calls_endpoint`, `produces`, `consumes`

# Empty output

If no candidates and no additional facts:

```json
{
  "candidate_decisions": [],
  "additional_facts": []
}
```

# Technology-specific files

For files matching technology-specific patterns (e.g., .prisma schema files), follow the technology instructions provided alongside this guide. They define how to extract models, relations, and framework-specific facts.
