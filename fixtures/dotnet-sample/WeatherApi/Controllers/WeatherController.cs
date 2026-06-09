using Microsoft.AspNetCore.Mvc;
using WeatherApi.Services;
using WeatherApi.Models;

namespace WeatherApi.Controllers;

[ApiController]
[Route("api/[controller]")]
public class WeatherController : ControllerBase
{
    private readonly IWeatherService _weatherService;
    private readonly INotificationService _notificationService;

    public WeatherController(IWeatherService weatherService, INotificationService notificationService)
    {
        _weatherService = weatherService;
        _notificationService = notificationService;
    }

    [HttpGet]
    public async Task<ActionResult<List<WeatherData>>> GetAll()
    {
        var data = await _weatherService.GetAllAsync();
        return Ok(data);
    }

    [HttpGet("{stationId}")]
    public async Task<ActionResult<WeatherData>> GetByStation(string stationId)
    {
        var data = await _weatherService.GetByStationAsync(stationId);
        if (data == null) return NotFound();
        return Ok(data);
    }

    [HttpPost]
    public async Task<ActionResult<WeatherData>> Report([FromBody] WeatherData data)
    {
        var saved = await _weatherService.RecordAsync(data);
        await _notificationService.NotifyWeatherUpdateAsync(saved);
        return CreatedAtAction(nameof(GetByStation), new { stationId = saved.StationId }, saved);
    }

    [HttpDelete("{stationId}")]
    public async Task<IActionResult> Delete(string stationId)
    {
        await _weatherService.DeleteAsync(stationId);
        return NoContent();
    }
}
