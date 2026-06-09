namespace WeatherApi.Models;

public class WeatherData
{
    public int Id { get; set; }
    public string StationId { get; set; } = string.Empty;
    public double Temperature { get; set; }
    public double Humidity { get; set; }
    public double WindSpeed { get; set; }
    public DateTime Timestamp { get; set; }

    public Station Station { get; set; } = null!;
}

public class Station
{
    public int Id { get; set; }
    public string StationId { get; set; } = string.Empty;
    public string Name { get; set; } = string.Empty;
    public double Latitude { get; set; }
    public double Longitude { get; set; }

    public List<WeatherData> Readings { get; set; } = new();
}
