using Microsoft.AspNetCore.Mvc;
using AlertsApi.Services;

namespace AlertsApi.Controllers;

[ApiController]
[Route("api/[controller]")]
public class AlertController : ControllerBase
{
    private readonly IAlertService _alertService;

    public AlertController(IAlertService alertService)
    {
        _alertService = alertService;
    }

    [HttpGet]
    public async Task<ActionResult<List<Alert>>> GetAll()
    {
        var alerts = await _alertService.GetActiveAlertsAsync();
        return Ok(alerts);
    }

    [HttpPost]
    public async Task<ActionResult<Alert>> Create([FromBody] CreateAlertRequest request)
    {
        var alert = await _alertService.CreateAlertAsync(request.StationId, request.Severity, request.Message);
        return CreatedAtAction(nameof(GetAll), alert);
    }

    [HttpPost("{id}/acknowledge")]
    public async Task<IActionResult> Acknowledge(int id)
    {
        await _alertService.AcknowledgeAsync(id);
        return NoContent();
    }
}

public record CreateAlertRequest(string StationId, string Severity, string Message);
