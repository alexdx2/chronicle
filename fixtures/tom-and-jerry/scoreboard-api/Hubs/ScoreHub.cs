using Microsoft.AspNetCore.SignalR;

namespace ScoreboardApi.Hubs;

/// <summary>
/// SignalR hub for live scoreboard updates.
/// Clients subscribe to score changes for specific fighters.
/// </summary>
public class ScoreHub : Hub
{
    public async Task WatchFighter(string fighterId)
    {
        await Groups.AddToGroupAsync(Context.ConnectionId, $"fighter-{fighterId}");
    }

    public async Task UnwatchFighter(string fighterId)
    {
        await Groups.RemoveFromGroupAsync(Context.ConnectionId, $"fighter-{fighterId}");
    }

    public async Task WatchLeaderboard()
    {
        await Groups.AddToGroupAsync(Context.ConnectionId, "leaderboard");
    }
}
