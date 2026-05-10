# FLOW TRACING

Trace business flows from entry points through reachable services.

Output MUST be a valid JSON array.

```json
[
  {
    "kind": "flow",
    "flow_name": "Place Order",
    "trigger": "POST /orders",
    "method": "createOrder",
    "requires": ["OrderService", "PaymentService"],
    "steps": [
      "Validate the order request",
      "Create the order record",
      "Request payment processing",
      "Publish order.created event"
    ]
  }
]
```

Do not output explanations.

# Inputs

You receive:
- trigger file content
- `flow_context.reachable` — services/nodes reachable from the trigger
- `flow_context.files_to_read` — source files to read for tracing
- Phase 1 facts (endpoints, injects, calls)

# Rules

## One flow per real entry point

A controller with 5 endpoints = up to 5 flows.
Do not create flows for private helper methods.

## trigger format

MUST match the actual entry point:
- `"POST /orders"` | `"GET /users/:id"`
- `"Mutation createOrder"` | `"Query getUser"`
- `"Subscription onOrder"`
- `"WS handleConnection"`
- `"order.created"` (consumer/event trigger)

Do not invent marketing-style trigger names.

## requires

MUST include only services present in `flow_context.reachable`.

Do NOT include: DTOs, models, enums, utilities, framework classes, services not in reachable.

## steps

Describe business behavior, not code syntax. 1-8 steps.

Good: "Create the order record", "Apply available vouchers", "Publish order.created event"
Bad: "Calls this.orderService.create(dto)", "Runs map() over items", "Returns Promise"

## Evidence rule

Only include behavior visible from:
- the trigger file
- files in `flow_context.files_to_read`
- Phase 1 facts

Do not infer hidden logic. Do not invent downstream calls or events.

## Empty output

If the trigger file has no meaningful business flow: return `[]`
