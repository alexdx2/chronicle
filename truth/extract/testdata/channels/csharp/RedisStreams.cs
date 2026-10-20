using StackExchange.Redis;

public class Bus
{
    async Task Run()
    {
        await db.StreamAddAsync("cs-stream-a", "k", "v");
        db.StreamAdd("cs-stream-b", "k", "v");
        await db.StreamReadGroupAsync("cs-stream-c", "g", "me", ">");
        await db.StreamReadAsync("cs-stream-d", "0-0");
        await sub.PublishAsync("cs-chan-a", "x");
        await sub.PublishAsync(RedisChannel.Literal("cs-chan-b"), "x");
        await sub.SubscribeAsync("cs-chan-c");
        await sub.SubscribeAsync(new RedisChannel("cs-chan-d", RedisChannel.PatternMode.Literal));
        // await sub.PublishAsync("cs-chan-commented", "x");
        await sub.PublishAsync(chanVar, "x");
        await db.StreamAddAsync(streamVar, "k", "v");
        _log.Info("sub.PublishAsync(\"cs-chan-logged\", x)");
    }
}
