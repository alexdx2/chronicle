using Microsoft.AspNetCore.SignalR;
using AlertsApi.Hubs;

namespace AlertsApi.Services;

public interface IAlertService
{
    Task<List<Alert>> GetActiveAlertsAsync();
    Task<Alert> CreateAlertAsync(string stationId, string severity, string message);
    Task AcknowledgeAsync(int id);
}

public class AlertService : IAlertService
{
    private readonly IHubContext<AlertHub> _hubContext;
    private static readonly List<Alert> _alerts = new();
    private static int _nextId = 1;

    public AlertService(IHubContext<AlertHub> hubContext)
    {
        _hubContext = hubContext;
    }

    public Task<List<Alert>> GetActiveAlertsAsync()
    {
        var active = _alerts.Where(a => !a.Acknowledged).ToList();
        return Task.FromResult(active);
    }

    public async Task<Alert> CreateAlertAsync(string stationId, string severity, string message)
    {
        var alert = new Alert
        {
            Id = _nextId++,
            StationId = stationId,
            Severity = severity,
            Message = message,
            CreatedAt = DateTime.UtcNow
        };
        _alerts.Add(alert);

        await _hubContext.Clients.All.SendAsync("NewAlert", alert);
        return alert;
    }

    public Task AcknowledgeAsync(int id)
    {
        var alert = _alerts.FirstOrDefault(a => a.Id == id);
        if (alert != null) alert.Acknowledged = true;
        return Task.CompletedTask;
    }
}

public class Alert
{
    public int Id { get; set; }
    public string StationId { get; set; } = string.Empty;
    public string Severity { get; set; } = string.Empty;
    public string Message { get; set; } = string.Empty;
    public DateTime CreatedAt { get; set; }
    public bool Acknowledged { get; set; }
}
