using WeatherApi.Models;

namespace WeatherApi.Services;

public interface INotificationService
{
    Task NotifyWeatherUpdateAsync(WeatherData data);
}

public class NotificationService : INotificationService
{
    private readonly HttpClient _httpClient;

    public NotificationService(IHttpClientFactory httpClientFactory)
    {
        _httpClient = httpClientFactory.CreateClient("AlertsApi");
    }

    public async Task NotifyWeatherUpdateAsync(WeatherData data)
    {
        if (data.Temperature > 40 || data.Temperature < -20)
        {
            await _httpClient.PostAsJsonAsync(
                "http://alerts-api/api/alerts",
                new { StationId = data.StationId, Severity = "high", Message = $"Extreme temperature: {data.Temperature}°C" }
            );
        }
    }
}
