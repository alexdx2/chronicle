using Confluent.Kafka;

public class Bus
{
    void Run()
    {
        consumer.Subscribe("orders-created");
        consumer.Subscribe(new[] { "payments", "refunds" });
        consumer.Subscribe(_options.Topic);
        // await producer.ProduceAsync("commented", msg);
        await producer.ProduceAsync("Order Shipped", msg);
        producer.Produce("audit-log", msg);
        consumer.Assign(new TopicPartition("inventory", 0));
        consumer.Subscribe($"orders-{env}");
        await producer.ProduceAsync("orders-" + env, msg);
    }
}
