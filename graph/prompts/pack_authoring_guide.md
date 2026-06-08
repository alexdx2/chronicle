# HOW TO CREATE AN INSTRUCTION PACK

When a project uses a framework, ORM, or language that has no matching instruction pack,
create one. The pack is saved to `.depbot/packs/` and used in all future scans.

## Pack structure

Every pack is a markdown file with this structure:

```markdown
# FRAMEWORK_NAME EXTRACTION RULES

## Match
Load this pack when:
- [file extensions, build files, imports, decorators that indicate this technology]
- [be specific — list concrete filenames, extensions, import paths, or patterns]

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

## Hierarchy — Parent Assignment

Describe how containment works in this framework and map to parent facts.

## Do NOT extract

List what to skip: admin classes, migrations, tests, config, generated code.
```

## The Match section

The `## Match` section is CRITICAL. It tells the scan system when this pack should be loaded.
Write it as a checklist of concrete signals an agent can verify by reading project files:

Good:
```
## Match
Load this pack when:
- .csproj or .sln files exist in the project
- Files have .cs extension
- Files use [ApiController], [HttpGet] attributes
- Files reference Microsoft.EntityFrameworkCore
```

Bad:
```
## Match
Load this pack for C# projects.
```

Be specific. List file extensions, filenames, import paths, decorator names, config keys.

## Rules for the pack

CRITICAL: Your pack MUST follow these rules:

1. **Only use existing fact kinds.** allowed kinds:
   `endpoint`, `injects`, `provides`, `calls_service`, `calls_endpoint`,
   `uses_model`, `http_call`, `model`, `enum`, `model_relation`,
   `produces`, `consumes`, `import`, `parent`, `declares_service`

2. **Never introduce new kinds.** If the framework has a concept that doesn't fit,
   map it to the closest existing kind:
   - Middleware/filters → treat as providers, extract their `injects` deps
   - Signals/tasks → `produces` or `consumes`
   - Scheduled jobs → treat as consumers
   - Decorators/annotations → what they MEAN, not what they ARE

3. **Be specific about patterns.** Show exact code → exact fact mapping.
   Bad: "Extract models from the file"
   Good: `class Order(models.Model):` → `{"kind":"model","to":"Order"}`

4. **Include reject guidance.** Tell the agent what NOT to extract:
   admin classes, test fixtures, migration files, generic utilities.

5. **Include hierarchy rules.** Describe how to emit `parent` facts:
   which component belongs to which container/module/project.

## How to create a pack (step by step)

1. Read 3-5 representative files that use the framework
2. Identify patterns: routes, DI, models, events, services
3. Write the pack following the structure above
4. Start with the `## Match` section — be concrete
5. Map each pattern to a core fact kind
6. Add a "Do NOT extract" section
7. Save with `chronicle_save_custom_pack(id="<tech-name>", content=<pack>)`

The pack is saved to `.depbot/packs/<tech-name>.md` and will be automatically
discovered in future scans.

## Examples

### Django example

```markdown
# DJANGO EXTRACTION RULES

## Match
Load this pack when:
- Files import from django.* (django.http, django.views, django.db)
- manage.py exists in the project root
- requirements.txt or pyproject.toml lists django
- Files have .py extension and use Django patterns

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

## Hierarchy — Parent Assignment

- Controllers/views → parent is the Django app (directory with views.py)
- Models → parent is the Django app
- Celery tasks → parent is the Django app containing the task

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

## Match
Load this pack when:
- pom.xml or build.gradle lists spring-boot dependencies
- Files import from org.springframework.*
- Files use annotations: @RestController, @Service, @Repository, @Component
- Files have .java or .kt extension with Spring patterns

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

## Hierarchy — Parent Assignment

- Controllers/Services/Repositories → parent is the module (directory with @Configuration or @SpringBootApplication)
- Entities → parent is the project

## Do NOT extract

- Spring configuration classes (@Configuration, @Bean)
- Test classes
- Migration/Flyway scripts
```
