public class Other
{
    void Run(IBus bus)
    {
        var s = bus.CreateSender("not-azure");
        var p = bus.CreateProcessor("no", "no");
    }
}
