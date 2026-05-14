# NESTJS EXTRACTION RULES

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

## Events

`this.eventEmitter.emit('order.created', payload)` => `{"kind":"produces","to":"order.created","method":"emit"}`

Event handlers with listener decorators => `{"kind":"consumes","to":"event.name","method":"handlerName"}`

## Guards / Interceptors / Pipes

These are architectural but secondary. Extract `injects` for their DI dependencies.
Do NOT create separate fact kinds for guards/pipes — they're providers.

## WebSocket Gateways

`@WebSocketGateway()` classes are controllers. Socket event handlers:
`@SubscribeMessage('joinRoom')` => `{"kind":"endpoint","method":"WS","target":"joinRoom"}`
