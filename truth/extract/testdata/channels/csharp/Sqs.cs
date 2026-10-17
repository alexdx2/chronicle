public class Q
{
    void Send()
    {
        var send = new SendMessageRequest { QueueUrl = "https://sqs.eu-west-1.amazonaws.com/123456789012/orders-queue", MessageBody = body };
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        var recv = new ReceiveMessageRequest
        {
            QueueUrl = "https://sqs.eu-west-1.amazonaws.com/123456789012/Inbox",
        };
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        // ------
        var other = new Thing { QueueUrl = "https://sqs.eu-west-1.amazonaws.com/123456789012/billing" };
        var q = await client.GetQueueUrlAsync("dead-letters");
        var v = new Thing { QueueUrl = _config.Url };
        var f = new PutEventsRequestEntry { DetailType = "Inv-" + kind };
        var e = new PutEventsRequestEntry { DetailType = "OrderPlaced", Source = "x" };
    }
}
