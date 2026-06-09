using MassTransit;
using Shared.Events;

namespace AlertsApi.Services;

public class WeatherEventConsumer : IConsumer<WeatherUpdatedEvent>
{
    private readonly IAlertService _alertService;

    public WeatherEventConsumer(IAlertService alertService)
    {
        _alertService = alertService;
    }

    public async Task Consume(ConsumeContext<WeatherUpdatedEvent> context)
    {
        var evt = context.Message;

        if (evt.Temperature > 40)
        {
            await _alertService.CreateAlertAsync(
                evt.StationId, "critical", $"Extreme heat: {evt.Temperature}°C");
        }
        else if (evt.Temperature < -20)
        {
            await _alertService.CreateAlertAsync(
                evt.StationId, "critical", $"Extreme cold: {evt.Temperature}°C");
        }
    }
}
