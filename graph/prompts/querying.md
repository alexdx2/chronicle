# Querying the Chronicle graph

You (the agent) are the brain. Chronicle's tools are deterministic retrieval —
they match strings and walk edges. They do NOT understand questions. Your job is
to translate a question into a small sequence of tool calls, read the typed,
trust-scored results, and answer.

**Prefer the graph over grep.** A scoped query reads a compact subgraph instead
of dozens of raw files. Reach for `grep`/`Read` only when the graph is missing
something — then consider scanning to fill the gap.

## The one rule: resolve names first

Every structured tool (`chronicle_query_deps`, `chronicle_query_reverse_deps`,
`chronicle_query_path`, `chronicle_impact`, `chronicle_subgraph`) needs an exact
`node_key`. **Never guess a node_key.** Call `chronicle_node_search` first to
turn a name fragment into a key.

```
chronicle_node_search(q="OrderController")
  → [{node_key: "code:controller:orders:order-controller", match_kind: "exact_name", ...}]
```

`node_search` is deterministic lexical matching (exact > glossary > prefix >
substring > file path). Multi-word queries are AND. Pass `layer`/`domain` to
narrow. If the top result's `match_kind` is `substring`/`path` (a weak match),
look down the list before committing.

## Question → tool composition

| The user asks… | Do this |
|---|---|
| "What breaks if I change **X**?" | `node_search(X)` → `chronicle_impact(node_key, depth=4)` → report `affected_surface` (endpoints, topics) and impacts with `impact_score ≥ 80` |
| "What does **X** depend on?" | `node_search(X)` → `chronicle_query_deps(node_key)` |
| "Who uses / calls **X**?" | `node_search(X)` → `chronicle_query_reverse_deps(node_key)` |
| "How does **A** connect to **B**?" | `node_search(A)`, `node_search(B)` → `chronicle_query_path(from, to)` |
| "Explain **X** / show me X's neighborhood" | `node_search(X)` → `chronicle_subgraph(node_key, depth=2)` |
| "Give me an overview / the architecture" | `chronicle_query_stats` → `chronicle_diagram_build` (or `chronicle_insights`) |
| "What's interesting / what should I verify?" | `chronicle_insights` |

## Efficiency

- Use **one** `chronicle_subgraph(depth=2)` instead of many `query_deps` calls
  when you want the neighborhood around a node. It returns nodes + typed edges in
  a single call and truncates lowest-trust periphery first (it tells you what it
  cut).
- Set `direction`: `out` = dependencies, `in` = dependents, `both` = full
  neighborhood.

## Report trust honestly

Every node and edge carries a `trust_score` derived from evidence. When an answer
rests on an edge with `trust_score < 0.7`, say so — that edge is an
uncorroborated or unverified inference, not an established fact. Don't launder a
guess into a confident claim. If a result is empty or surprisingly small, the
graph may be incomplete or stale — check `chronicle_status` for staleness before
falling back to reading source.
