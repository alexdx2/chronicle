# HOW TO CREATE A CUSTOM INSTRUCTION PACK

When a project uses a framework, ORM, or language that has no built-in instruction pack,
you can create a custom pack. Follow this guide exactly.

## Step 1: Understand the technology

Read 3-5 representative files that use the framework. Identify:
- How entry points are defined (routes, handlers, controllers, views)
- How dependency injection works (constructors, decorators, annotations, imports)
- How data models are defined (ORM models, schemas, entities)
- How services/components call each other
- How events/messages are published and consumed
- How database queries are made

## Step 2: Write the pack

The pack is a markdown file. Structure it like this:

```markdown
# FRAMEWORK_NAME EXTRACTION RULES

Apply when [conditions: file uses X decorators/imports/patterns].

## Entry points (→ endpoint facts)

Describe how the framework defines routes/handlers and map them to endpoint facts.

Example:
`@app.route('/orders', methods=['POST'])` => `{"kind":"endpoint","method":"POST","target":"/orders"}`

## Dependency injection (→ injects facts)

Describe how DI works and map to injects facts.

## Data models (→ model/enum facts)

Describe ORM model definitions and map to model, enum, model_relation facts.

## Model usage (→ uses_model facts)

Describe how code queries/persists data and map to uses_model facts.

## Service calls (→ calls_service facts)

Describe how services call each other and map to calls_service facts.

## Events (→ produces/consumes facts)

Describe event publishing/consuming patterns and map to produces/consumes facts.

## HTTP calls (→ http_call facts)

Describe outbound HTTP call patterns.
```

## Step 3: Rules for the pack

CRITICAL: Your pack MUST follow these rules:

1. **Only use existing fact kinds.** Allowed kinds:
   `endpoint`, `injects`, `provides`, `calls_service`, `calls_endpoint`,
   `uses_model`, `http_call`, `model`, `enum`, `model_relation`,
   `produces`, `consumes`, `import`

2. **Never introduce new kinds.** If the framework has a concept that doesn't fit
   (e.g. "middleware", "signal", "task"), map it to the closest existing kind:
   - Middleware/filters → treat as providers, extract their `injects` deps
   - Signals/tasks → `produces` or `consumes`
   - Scheduled jobs → treat as consumers with trigger = cron pattern
   - Decorators/annotations → what they MEAN, not what they ARE

3. **Be specific about patterns.** Show exact code → exact fact mapping.
   Bad: "Extract models from the file"
   Good: `class Order(models.Model):` → `{"kind":"model","to":"Order"}`

4. **Include reject guidance.** Tell the agent what NOT to extract:
   - Admin/settings classes
   - Test fixtures
   - Migration files
   - Generic utilities

## Step 4: Examples for common frameworks

### Django example

```markdown
# DJANGO EXTRACTION RULES

Apply when file imports from `django.*` or uses Django patterns.

## Views (→ endpoint facts)

URL patterns in urls.py:
`path('orders/', views.create_order, name='create-order')` => `{"kind":"endpoint","method":"POST","target":"/orders"}`

Class-based views:
`class OrderView(APIView):` with `def post(self, request):` => `{"kind":"endpoint","method":"POST","target":"/orders"}`

DRF ViewSets:
`class OrderViewSet(ModelViewSet):` => endpoints for list/create/retrieve/update/destroy

## Models (→ model/enum facts)

`class Order(models.Model):` => `{"kind":"model","to":"Order"}`
`class OrderStatus(models.TextChoices):` => `{"kind":"enum","to":"OrderStatus"}`
`user = models.ForeignKey(User)` => `{"kind":"model_relation","from":"Order","to":"User"}`

## ORM usage (→ uses_model facts)

`Order.objects.filter(...)` => `{"kind":"uses_model","to":"Order"}`
`Order.objects.create(...)` => `{"kind":"uses_model","to":"Order"}`

## Service calls

`self.payment_service.charge(order)` => `{"kind":"calls_service","to":"PaymentService","method":"charge"}`

## Celery tasks (→ produces/consumes)

`order_created.delay(order_id)` => `{"kind":"produces","to":"order_created","method":"delay"}`
`@shared_task def process_order(order_id):` => `{"kind":"consumes","to":"process_order","method":"process_order"}`

## Do NOT extract

- Django admin classes
- Migration files
- Management commands (unless they have business logic)
- Test factories/fixtures
- Settings/config files
```

### Spring Boot example

```markdown
# SPRING BOOT EXTRACTION RULES

Apply when file imports from `org.springframework.*`.

## Controllers (→ endpoint facts)

`@GetMapping("/orders/{id}")` on a `@RestController` class => `{"kind":"endpoint","method":"GET","target":"/orders/{id}"}`
Combine class-level `@RequestMapping("/api")` with method-level mapping.

## DI (→ injects facts)

`@Autowired OrderService orderService` => `{"kind":"injects","to":"OrderService"}`
Constructor injection: `public OrderController(OrderService service)` => `{"kind":"injects","to":"OrderService"}`

## JPA entities (→ model facts)

`@Entity class Order` => `{"kind":"model","to":"Order"}`
`@ManyToOne User user` => `{"kind":"model_relation","from":"Order","to":"User"}`

## Repository usage (→ uses_model)

`orderRepository.findById(id)` => `{"kind":"uses_model","to":"Order"}`

## Events

`applicationEventPublisher.publishEvent(new OrderCreated(order))` => `{"kind":"produces","to":"OrderCreated","method":"publishEvent"}`
`@EventListener public void handle(OrderCreated event)` => `{"kind":"consumes","to":"OrderCreated","method":"handle"}`
```

## Step 5: Save the pack

After writing the pack, the system will validate it:
- All `"kind"` values must be from the allowed list
- The pack should have clear pattern → fact mappings
- The pack should include reject guidance

The pack will be saved to the project's settings and loaded during future scans.
