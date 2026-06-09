# KAFKA EXTRACTION RULES

## CRITICAL — topics are NOT HTTP endpoints

**NEVER** emit `{"kind":"endpoint", ...}` for a Kafka topic name.
`battle-results`, `tom.weapon.equipped` → `produces` / `consumes` only.

## Match
Load this pack when:
- package.json has `kafkajs` dependency
- .csproj has `Confluent.Kafka` PackageReference
- Files import from `kafkajs` or `Confluent.Kafka`
- Files import `EventPattern` from `@nestjs/microservices`
- Files contain Kafka topic strings or producer/consumer patterns
- Files extend `BackgroundService` and use `ConsumerBuilder`

## Events — producing (→ produces facts)

### NestJS / kafkajs
Producer service that sends to a Kafka topic:
`this.kafka.producer.send({ topic: 'battle-results', messages: [...] })` =>
`{"kind":"produces","to":"battle-results","method":"send"}`

Injectable producer class with a topic constant:
```typescript
const TOPIC = 'battle-results';
@Injectable()
export class BattleResultProducer {
  async publish(event) { /* sends to TOPIC */ }
}
```
=> `{"kind":"produces","to":"battle-results","method":"publish"}`

### .NET / Confluent.Kafka
`producer.ProduceAsync("battle-results", message)` =>
`{"kind":"produces","to":"battle-results","method":"ProduceAsync"}`

## Events — consuming (→ consumes facts)

### NestJS / @nestjs/microservices
`@EventPattern('battle-results')` decorator on a method =>
`{"kind":"consumes","to":"battle-results","method":"handleBattleResult"}`

### .NET / Confluent.Kafka
`consumer.Subscribe("battle-results")` in a BackgroundService =>
`{"kind":"consumes","to":"battle-results","method":"ExecuteAsync"}`

## Dependency injection (→ injects facts)

### NestJS
Constructor injection in consumer/producer classes:
`constructor(private readonly spectatorService: SpectatorService)` =>
`{"kind":"injects","to":"SpectatorService"}`

### .NET
Constructor injection in BackgroundService consumers:
`public BattleResultConsumer(IServiceScopeFactory scopeFactory)` =>
`{"kind":"injects","to":"IServiceScopeFactory"}`

Scoped service resolution inside consumers:
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
