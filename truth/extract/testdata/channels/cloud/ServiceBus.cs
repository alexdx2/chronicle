using Azure.Messaging.ServiceBus;

public class Bus
{
    void Run(ServiceBusClient client, string dyn)
    {
        var p1 = client.CreateProcessor("topic-a", "sub-a", new ServiceBusProcessorOptions { MaxConcurrentCalls = 2 });
        var p2 = client.CreateProcessor("queue-b");
        var p3 = client.CreateProcessor("queue-c", new ServiceBusProcessorOptions());
        var r1 = client.CreateReceiver(
            "topic-d",
            "sub-d");
        var r2 = client.CreateReceiver(queueName: "queue-e");
        var r3 = client.CreateReceiver(topicName: "topic-f", subscriptionName: "sub-f");
        var s1 = client.CreateSender("queue-g");
        var s2 = client.CreateSender(topicName: "topic-h");
        // var no = client.CreateProcessor("commented", "out");
        log.LogInformation("sibling CreateSender(\"in-a-log\") handled elsewhere");
        var d1 = client.CreateSender(dyn);
        var d2 = client.CreateSender("prefix-" + dyn);
        var d3 = client.CreateProcessor($"interp-{dyn}");
        var d4 = client.CreateProcessor("topic-x", subName);
    }
}
