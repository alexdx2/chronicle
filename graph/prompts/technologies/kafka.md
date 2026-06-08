# KAFKA / EVENT STREAM EXTRACTION RULES

## Match
Load this pack when:
- package.json has `kafkajs` dependency
- .csproj has `Confluent.Kafka` PackageReference
- Files import from `kafkajs` or `Confluent.Kafka`
- Files import `EventPattern` from `@nestjs/microservices`
- Files contain Kafka topic strings or producer/consumer patterns
- Files extend `BackgroundService` and use `ConsumerBuilder`

## CRITICAL — topics are NOT HTTP endpoints

**NEVER** emit `{"kind":"endpoint", ...}` for a Kafka topic name.

Topic names like `battle-results`, `tom.weapon.equipped`, `order.created` are **event contracts**:
- Emit `{"kind":"produces","to":"<topic>"}` or `{"kind":"consumes","to":"<topic>"}`
- The resolver creates `contract:topic:*` nodes — not REST endpoints

If you see `ProduceAsync("battle-results")` or `@EventPattern('battle-results')`, that is messaging — not GET /battle-results.

## Events — producing (→ produces facts)

### NestJS / kafkajs
`this.kafka.producer.send({ topic: 'battle-results', messages: [...] })` =>
`{"kind":"produces","to":"battle-results","method":"send"}`

### .NET / Confluent.Kafka
`producer.ProduceAsync("battle-results", message)` =>
`{"kind":"produces","to":"battle-results","method":"ProduceAsync"}`

## Events — consuming (→ consumes facts)

### NestJS / @nestjs/microservices
`@EventPattern('battle-results')` =>
`{"kind":"consumes","to":"battle-results"}`

### .NET / Confluent.Kafka
`consumer.Subscribe("battle-results")` in a BackgroundService =>
`{"kind":"consumes","to":"battle-results","method":"Subscribe"}`

## Dependency injection (→ injects facts)

Constructor injection in consumer/producer classes:
`constructor(private readonly spectatorService: SpectatorService)` =>
`{"kind":"injects","to":"SpectatorService"}`

.NET scoped resolution inside consumers:
`scope.ServiceProvider.GetRequiredService<IScoreService>()` =>
`{"kind":"calls_service","to":"IScoreService","method":"RecordBattleResultAsync"}`

## Hierarchy — Parent Assignment

- Kafka producers → parent is the module that provides them
- Kafka consumers → parent is the module that provides them
- .NET BackgroundService consumers → parent is the project/assembly

## Do NOT extract

- Kafka configuration objects (ConsumerConfig, ProducerConfig)
- Serializer/deserializer setup
- Error handling / retry logic
- Logging statements
- Test consumers/producers
