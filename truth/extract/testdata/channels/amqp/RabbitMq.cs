using RabbitMQ.Client;

public class Bus
{
    void Run(IModel channel, IChannel ch2)
    {
        channel.ExchangeDeclare(exchange: "ledger.events", type: ExchangeType.Fanout);
        channel.QueueDeclare(queue: "ledger.in", durable: true, exclusive: false);
        channel.QueueBind(queue: "ledger.in", exchange: "ledger.events", routingKey: "");
        channel.BasicConsume(queue: "ledger.in", autoAck: true, consumer: null);
        channel.BasicConsume("lonely", true, null);
        channel.BasicPublish(exchange: "ledger.events", routingKey: "x", basicProperties: null, body: null);
        channel.BasicPublish("", "work.queue", null, null);
        await ch2.BasicPublishAsync("alerts", "k", body);
        // channel.QueueDeclare("commented");
        _log.LogInformation("BasicConsume(\"in-a-log\")");
        channel.QueueDeclare(_opts.Queue);
        channel.QueueDeclare("");
    }
}
