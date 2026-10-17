using System.Net.Http;

public static class Wiring
{
    public static void Configure(IServiceCollection services)
    {
        services.AddHttpClient<IPayments, PaymentsClient>(c =>
            c.BaseAddress = new Uri("https://api.payments.io/v1"));
        services.AddHttpClient("tom", c =>
        {
            c.BaseAddress = new("http://TOM-API.internal:8080/");
        });
        services.AddHttpClient("local", c => c.BaseAddress = new Uri("http://localhost:5000"));
        // services.AddHttpClient("old", c => c.BaseAddress = new Uri("https://old.example.com"));
        var raw = new HttpClient { BaseAddress = new Uri("https://raw.example.com") };
    }

    public async Task Run(HttpClient client)
    {
        await client.GetAsync("https://reports.example.com/daily");
        await client.PostAsync($"https://hooks.example.com/{id}", body);
        await client.GetAsync("/relative/path");
        await client.GetAsync("http://127.0.0.1:9000/x");
        var api = RestService.For<IShipping>("https://ship.example.com");
        // client.GetAsync("https://commented.example.com/");
    }
}
