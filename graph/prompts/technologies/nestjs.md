# NESTJS EXTRACTION RULES

Apply when the project uses NestJS or the file uses NestJS decorators/imports.

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

`@Module({ providers: [OrderService] })` => `{"kind":"provides","to":"OrderService"}`

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
