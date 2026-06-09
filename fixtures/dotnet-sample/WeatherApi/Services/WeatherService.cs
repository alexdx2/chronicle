using Microsoft.EntityFrameworkCore;
using WeatherApi.Data;
using WeatherApi.Models;
using MassTransit;
using Shared.Events;

namespace WeatherApi.Services;

public class WeatherService : IWeatherService
{
    private readonly WeatherDbContext _context;
    private readonly IPublishEndpoint _publishEndpoint;

    public WeatherService(WeatherDbContext context, IPublishEndpoint publishEndpoint)
    {
        _context = context;
        _publishEndpoint = publishEndpoint;
    }

    public async Task<List<WeatherData>> GetAllAsync()
    {
        return await _context.WeatherData.ToListAsync();
    }

    public async Task<WeatherData?> GetByStationAsync(string stationId)
    {
        return await _context.WeatherData
            .FirstOrDefaultAsync(w => w.StationId == stationId);
    }

    public async Task<WeatherData> RecordAsync(WeatherData data)
    {
        _context.WeatherData.Add(data);
        await _context.SaveChangesAsync();

        await _publishEndpoint.Publish(new WeatherUpdatedEvent
        {
            StationId = data.StationId,
            Temperature = data.Temperature,
            Timestamp = data.Timestamp
        });

        return data;
    }

    public async Task DeleteAsync(string stationId)
    {
        var entity = await _context.WeatherData
            .FirstOrDefaultAsync(w => w.StationId == stationId);
        if (entity != null)
        {
            _context.WeatherData.Remove(entity);
            await _context.SaveChangesAsync();
        }
    }
}
