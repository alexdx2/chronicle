# KAFKA EXTRACTION RULES

## Match
Load this pack when:
- package.json has `kafkajs` dependency
- .csproj has `Confluent.Kafka` PackageReference
- Files import from `kafkajs` or `@nestjs/microservices` with `@EventPattern`
- Files import from `Confluent.Kafka`
- Files reference Kafka topics, producers, or consumers

## Producers (→ produces facts)

### NestJS/TypeScript pattern
A class that sends messages to a Kafka topic:
```typescript
const TOPIC = 'battle-results';
@Injectable()
export class BattleResultProducer {
  async publish(event: any) {
    // kafka.producer.send({ topic: TOPIC, messages: [...] })
  }
}
```
=> `{"kind":"produces","to":"battle-results","method":"publish"}`

Key signals:
- Variable or string literal naming a topic (e.g. `const TOPIC = 'order-events'`)
- Methods like `send`, `publish`, `emit` that reference the topic
- kafkajs `producer.send({ topic: ... })`

### .NET/C# pattern
```csharp
producer.ProduceAsync("battle-results", new Message<string, string> { ... });
```
=> `{"kind":"produces","to":"battle-results","method":"ProduceAsync"}`

## Consumers (→ consumes facts)

### NestJS/TypeScript pattern
Using `@EventPattern` decorator from `@nestjs/microservices`:
```typescript
@EventPattern('battle-results')
handleBattleResult(event: any) { ... }
```
=> `{"kind":"consumes","to":"battle-results","method":"handleBattleResult"}`

### .NET/C# pattern
A `BackgroundService` that subscribes to a topic:
```csharp
consumer.Subscribe("battle-results");
var result = consumer.Consume(stoppingToken);
```
=> `{"kind":"consumes","to":"battle-results","method":"ExecuteAsync"}`

Key signals:
- `consumer.Subscribe("topic-name")`
- `ConsumerConfig` with `GroupId`
- Class inherits `BackgroundService` or `IHostedService`

## Topic identification

The topic name is the `to` value. Extract it from:
- String literals in `@EventPattern('...')`
- String constants (`const TOPIC = '...'`)
- `consumer.Subscribe("...")`
- `producer.send({ topic: '...' })`
- XML comments mentioning `Topic: ...`

## Hierarchy — Parent Assignment

- Kafka producers → parent is the module containing them
- Kafka consumers → parent is the module containing them
- In NestJS: parent is the `@Module` that lists the class in `providers`
- In .NET: parent is the project (namespace root)

## Do NOT extract

- Kafka configuration classes (just config, no business logic)
- Consumer group IDs as separate entities
- Connection strings / bootstrap server addresses
- Test consumers/producers in test files
