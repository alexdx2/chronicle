namespace Shared.Events;

public class WeatherUpdatedEvent
{
    public string StationId { get; set; } = string.Empty;
    public double Temperature { get; set; }
    public DateTime Timestamp { get; set; }
}
