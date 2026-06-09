using Microsoft.AspNetCore.SignalR;

namespace AlertsApi.Hubs;

public class AlertHub : Hub
{
    public async Task JoinStation(string stationId)
    {
        await Groups.AddToGroupAsync(Context.ConnectionId, $"station-{stationId}");
    }

    public async Task LeaveStation(string stationId)
    {
        await Groups.RemoveFromGroupAsync(Context.ConnectionId, $"station-{stationId}");
    }
}
