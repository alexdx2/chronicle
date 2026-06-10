# C# / .NET EXTRACTION RULES

## Match
Load this pack when:
- .csproj or .sln files exist in the project
- Files have .cs extension
- Files use attributes: [ApiController], [HttpGet], [HttpPost], [Route]
- Files reference Microsoft.EntityFrameworkCore, MassTransit, SignalR
- Files use ASP.NET Core patterns (ControllerBase, IHostedService, DbContext)

Apply when the project uses ASP.NET Core, .NET, or the file uses C# syntax and .NET framework imports.

## Service declaration (.csproj)

When processing a `.csproj` file, emit a `declares_service` fact with the assembly name:
`<AssemblyName>OrdersApi</AssemblyName>` => `{"kind":"declares_service","to":"OrdersApi"}`

If no AssemblyName, use the filename without extension:
`Orders.Api.csproj` => `{"kind":"declares_service","to":"Orders.Api"}`

## Controllers

`[ApiController] [Route("api/[controller]")]` + `[HttpGet("{id}")]` => `{"kind":"endpoint","method":"GET","target":"/api/orders/{id}"}`

Resolve `[controller]` placeholder from class name (strip "Controller" suffix, lowercase).
Combine controller-level `[Route]` and method-level `[Http*]` attributes.
Supported: [HttpGet], [HttpPost], [HttpPut], [HttpPatch], [HttpDelete].

Minimal API endpoints:
`app.MapGet("/orders/{id}", handler)` => `{"kind":"endpoint","method":"GET","target":"/orders/{id}"}`

## Dependency Injection

Constructor parameters = DI dependencies:
`public OrderService(IOrderRepository repo, ILogger<OrderService> logger)` => `{"kind":"injects","to":"IOrderRepository"}`

Do NOT emit injects for framework types: ILogger, IConfiguration, IOptions, IMediator, IMapper, IHttpClientFactory, IMemoryCache, IDistributedCache, IServiceProvider, IHostEnvironment.

Calls through injected deps:
`_repo.GetByIdAsync(id)` => `{"kind":"calls_service","to":"IOrderRepository","method":"GetByIdAsync"}`

Only emit `calls_service` if receiver is an injected dependency.

## Program.cs / Startup.cs — Service Registration & Composition Root

`builder.Services.AddScoped<IOrderService, OrderService>()` — registers DI; do NOT emit `injects` here.
The injection site (constructor) is what matters for `injects` facts.

**Composition root facts** — emit these from Program.cs / Startup.cs when present:

`builder.Services.AddScoped<IBattleService, BattleService>()` — no fact (DI registration only).

`app.MapControllers()` with `[ApiController]` classes elsewhere — endpoints come from controller files.

`app.MapHub<ScoreHub>("/score")` =>
`{"kind":"endpoint","method":"WS","target":"/score","from_type":"controller"}`

`builder.Services.AddHostedService<BattleResultConsumer>()` — consumer is a provider; extract `consumes`/`produces` from the hosted service class file, not Program.cs.

When a `.csproj` is scanned, always emit:
`{"kind":"declares_service","to":"<AssemblyName or project name>"}`

When the file is the ASP.NET entry point and wires the app, emit module backbone:
`{"kind":"provides","from_type":"module","reason":"ASP.NET composition root"}`

## Modules / project structure

For NestJS-style module files in C# (rare) or explicit assembly wiring:
- Files under `*Module.cs` or containing `IServiceCollection` extension methods that register a feature area =>
  `{"kind":"provides","from_type":"module"}`

Project folder ownership — when a controller/service clearly belongs to one .csproj:
`Orders.Api/Controllers/OrderController.cs` =>
`{"kind":"parent","to":"Orders.Api","reason":"file under Orders.Api project"}`

## Entity Framework Core

DbContext classes define data models:
`public DbSet<Order> Orders { get; set; }` => `{"kind":"model","to":"Order"}`

Entity configuration:
`modelBuilder.Entity<Order>()` => already covered by DbSet.

Relations are emitted from the FOREIGN-KEY side only (the model that holds the FK):
`OrderItem` has `public int OrderId { get; set; }` => `{"kind":"model_relation","from":"OrderItem","to":"Order"}`
Do NOT also emit the reverse direction for the collection navigation property
(`Order.Items`) — one relation, FK side wins. Same rule as Prisma scans.

Entity class files (`Models/*.cs`, plain POCO entities) get `"from_type": "model"` —
they are data definitions, not providers.

EF usage in code:
`_context.Orders.FindAsync(id)` => `{"kind":"uses_model","to":"Order"}`

Do NOT emit `uses_model` for entity class definitions — only for query/persist operations via DbContext.

## MediatR / CQRS

Request handlers are providers:
`public class CreateOrderHandler : IRequestHandler<CreateOrderCommand, OrderDto>` => extract as provider

The command/query being handled:
`IRequestHandler<CreateOrderCommand, OrderDto>` => `{"kind":"consumes","to":"CreateOrderCommand"}`

Sending commands:
`_mediator.Send(new CreateOrderCommand(...))` => `{"kind":"produces","to":"CreateOrderCommand"}`

Notification handlers:
`INotificationHandler<OrderCreatedEvent>` => `{"kind":"consumes","to":"OrderCreatedEvent"}`
`_mediator.Publish(new OrderCreatedEvent(...))` => `{"kind":"produces","to":"OrderCreatedEvent"}`

## SignalR Hubs

`public class ScoreHub : Hub` — treat as controller (WebSocket surface).
MapHub in Program.cs defines the hub route — prefer that path for the endpoint fact.

Constructor injection in hubs:
`public ScoreHub(IBattleService battles, ILogger<ScoreHub> log)` =>
`{"kind":"injects","to":"IBattleService"}` (skip ILogger)

`IHubContext<XHub>` injection means the class pushes through that hub — emit the
hub class, not the wrapper: `IHubContext<ScoreHub> hub` => `{"kind":"injects","to":"ScoreHub"}`.

Hub methods exposed to clients:
`public async Task JoinRoom(string room)` => `{"kind":"endpoint","method":"WS","target":"JoinRoom"}`

Client invocations (server push):
`Clients.All.SendAsync("BattleUpdate", data)` => `{"kind":"produces","to":"BattleUpdate","method":"SendAsync"}`

## Background Services

`public class CleanupWorker : BackgroundService` or `IHostedService` — treat as provider.
Extract any service calls, event consumption, or scheduled operations from `ExecuteAsync`.

Service-locator resolution counts as injection:
`scope.ServiceProvider.GetRequiredService<IScoreService>()` => `{"kind":"injects","to":"IScoreService"}`

## Kafka (Confluent.Kafka)

The TOPIC STRING is the consumes/produces target — never the payload class.
`consumer.Subscribe("battle-results")` => `{"kind":"consumes","to":"battle-results"}`
`producer.ProduceAsync("battle-results", ...)` => `{"kind":"produces","to":"battle-results"}`
`JsonSerializer.Deserialize<BattleResultEvent>(...)` is the payload type — do NOT
emit a consumes fact for it.

## Events / Messaging

For message bus integration (MassTransit, NServiceBus, CAP):
- `IConsumer<OrderCreated>` => `{"kind":"consumes","to":"OrderCreated"}`
- `_publishEndpoint.Publish(new OrderCreated())` => `{"kind":"produces","to":"OrderCreated"}`
- `_bus.Send(new ProcessOrder())` => `{"kind":"produces","to":"ProcessOrder"}`

For domain events:
- `AddDomainEvent(new OrderCreated())` => `{"kind":"produces","to":"OrderCreated"}`

## Hierarchy — Parent Assignment

Emit a `parent` fact ONLY when you have explicit evidence of containment.
Do NOT guess. If unsure, omit the parent fact entirely.

Rules (use the MOST SPECIFIC match):

1. **Controller/Service/Handler in a feature folder**
   - Parent = the project (determined from .csproj boundary)
   - Determine from file path: `Orders.Api/Controllers/OrderController.cs` -> parent is `Orders.Api`
   - Emit: `{"kind": "parent", "to": "Orders.Api", "reason": "file under Orders.Api/ project"}`

2. **Entity/Model classes**
   - Parent = the project that owns them
   - `Orders.Domain/Entities/Order.cs` -> parent is `Orders.Domain`
   - Emit: `{"kind": "parent", "to": "Orders.Domain", "reason": "entity under Orders.Domain/"}`

3. **Shared library files** (in `Shared/`, `Common/`, `BuildingBlocks/`)
   - Parent = the project name from .csproj
   - Emit: `{"kind": "parent", "to": "Shared.Contracts", "reason": "shared library"}`

4. **Ambiguous cases** — do NOT emit parent:
   - Classes in root namespace with no clear project ownership
   - Generated code (obj/, bin/)
   - Test projects

## Cross-service HTTP calls

When a service uses HttpClient or IHttpClientFactory to call another service:
`_httpClient.GetAsync("http://orders-api/api/orders")` => `{"kind":"calls_endpoint","method":"GET","target":"/api/orders"}` + `{"kind":"calls_service","to":"orders-api"}`

gRPC calls:
`_orderClient.GetOrderAsync(request)` => `{"kind":"calls_service","to":"OrderService","method":"GetOrderAsync"}`

Set `derivation_kind: "linked"` for cross-service calls.

## Middleware / Filters

`IActionFilter`, `IMiddleware`, `IExceptionFilter` — these are providers.
Extract `injects` for their DI dependencies.
Do NOT create separate fact kinds for filters — they're providers.

## What to SKIP

- `using` statements (imports) — do NOT emit `import` facts for `using System.*` or `using Microsoft.*`
- DO emit `import` facts for project-to-project references (`using Orders.Domain.Entities`)
- Attribute definitions themselves (not usage)
- Extension methods on primitives
- LINQ expressions on in-memory collections
- Test classes and test methods
