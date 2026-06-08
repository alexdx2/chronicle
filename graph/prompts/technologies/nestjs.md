# NESTJS EXTRACTION RULES

## Match
Load this pack when:
- package.json has @nestjs/* dependencies (@nestjs/common, @nestjs/core, etc.)
- Files import from @nestjs/common, @nestjs/core, @nestjs/graphql, @nestjs/websockets
- Files use decorators: @Controller, @Injectable, @Module, @Get, @Post, @Guard

Apply when the project uses NestJS or the file uses NestJS decorators/imports.

## Service declaration (package.json)

When processing a `package.json` file, emit a `declares_service` fact with the package name:
`{"name": "orders-api"}` => `{"kind":"declares_service","to":"orders-api"}`

This creates a service-layer node representing the deployable unit.

## Cross-service HTTP calls

When a provider makes HTTP calls to another service via injected HttpService, axios, or env-based URLs:
`this.httpService.get(process.env.TOM_API_URL + '/tom/status')` => `{"kind":"calls_endpoint","method":"GET","target":"/tom/status"}` + `{"kind":"calls_service","to":"tom-api"}`

Set `derivation_kind: "linked"` — these are inferred from env vars, not hard imports.

## Controllers

`@Controller('users')` + `@Get(':id')` => `{"kind":"endpoint","method":"GET","target":"/users/:id"}`

Combine controller-level and method-level paths. Supported: @Get, @Post, @Put, @Patch, @Delete, @All.

## Providers / Services

Constructor parameters in `@Injectable()` classes = DI dependencies:
`constructor(private orderService: OrderService)` => `{"kind":"injects","to":"OrderService"}`

Calls through injected deps = service calls:
`this.orderService.create(dto)` => `{"kind":"calls_service","to":"OrderService","method":"create"}`

Only emit `calls_service` if receiver is known to be an injected provider.

## Modules

CRITICAL: Every `@Module()` file MUST emit `provides` facts for ALL controllers AND providers declared in it.
CONTAINS edges (module→controller, module→provider) are built from these facts — if you omit them the graph will have no structural edges.

`@Module({ controllers: [OrderController] })` => `{"kind":"provides","to":"OrderController"}`
`@Module({ providers: [OrderService, OrderRepository] })` => `{"kind":"provides","to":"OrderService"}` + `{"kind":"provides","to":"OrderRepository"}`

Emit one `provides` fact per entry in `controllers:` and `providers:` arrays.
Set `from_type: "module"` on the file so the resolver creates CONTAINS edges (module→controller, module→provider).

Do NOT treat `imports: [OtherModule]` as `calls_service`.

## Hierarchy — Parent Assignment

Emit a `parent` fact ONLY when you have explicit evidence of containment.
Do NOT guess. If unsure, omit the parent fact entirely.

Rules (use the MOST SPECIFIC match):

1. **Controller/Provider/Guard/Interceptor/Gateway**
   - Parent = the @Module that DECLARES this class in its controllers/providers array
   - NOT the module that imports or injects it — only the declaring module
   - Evidence: `@Module({ controllers: [ThisClass] })` or `@Module({ providers: [ThisClass] })`
   - Emit: `{"kind": "parent", "to": "arena.module", "reason": "declared in @Module.controllers"}`
   - If class is in a shared/common directory with no clear owning module, omit parent

2. **@Module class itself**
   - Parent = the service (directory with its own package.json)
   - Determine from file path: `arena-api/src/arena/arena.module.ts` → parent is `arena-api`
   - Emit: `{"kind": "parent", "to": "arena-api", "reason": "module file under arena-api/"}`

3. **Prisma schema / model files**
   - Parent = the service that owns the prisma directory
   - `arena-api/prisma/schema.prisma` → parent is `arena-api`
   - Emit: `{"kind": "parent", "to": "arena-api", "reason": "prisma dir under arena-api/"}`

4. **Shared library files** (in `shared/`, `libs/`, `packages/`)
   - Parent = the package name from package.json
   - Emit: `{"kind": "parent", "to": "shared", "reason": "shared package"}`

5. **Ambiguous cases** — do NOT emit parent:
   - Provider used by multiple modules with no clear declaration
   - Barrel re-exports
   - Dynamic modules
   - Files in root `src/` with no clear module ownership

## Events

`this.eventEmitter.emit('order.created', payload)` => `{"kind":"produces","to":"order.created","method":"emit"}`

Event handlers with listener decorators => `{"kind":"consumes","to":"event.name","method":"handlerName"}`

## Guards / Interceptors / Pipes

These are architectural but secondary. Extract `injects` for their DI dependencies.
Do NOT create separate fact kinds for guards/pipes — they're providers.

## WebSocket Gateways

`@WebSocketGateway()` classes are controllers. Socket event handlers:
`@SubscribeMessage('joinRoom')` => `{"kind":"endpoint","method":"WS","target":"joinRoom"}`
