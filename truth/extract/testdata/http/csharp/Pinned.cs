public static class Pinned
{
    static void A(HttpClient c)
    {
        c.BaseAddress = GetUri();
        c.BaseAddress = new Uri("https://b.example.com");
    }
}
