using WeatherApi.Models;

namespace WeatherApi.Services;

public interface IWeatherService
{
    Task<List<WeatherData>> GetAllAsync();
    Task<WeatherData?> GetByStationAsync(string stationId);
    Task<WeatherData> RecordAsync(WeatherData data);
    Task DeleteAsync(string stationId);
}
