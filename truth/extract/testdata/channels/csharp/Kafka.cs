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
        consumer.Subscribe(new[] { "pre-" + env, "kept-cs" });
        consumer.Seek(new TopicPartitionOffset("seek-ignored", 0, 5));
        consumer.Commit(new[] { new TopicPartition("committed", 0) });
        await producer.ProduceAsync(new TopicPartition("tp-out", 1), msg);
        var tp = new TopicPartition("bare-tp", 2);
    }
}
