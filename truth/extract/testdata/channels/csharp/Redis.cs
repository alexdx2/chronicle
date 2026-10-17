using StackExchange.Redis;

public class Chat
{
    void Run()
    {
        sub = redis.GetSubscriber();
        sub.Subscribe("chat-channel");
    }
}
