# SIGNALR EXTRACTION RULES

## Match
Load this pack when:
- .csproj has `Microsoft.AspNetCore.SignalR` PackageReference
- Files import `Microsoft.AspNetCore.SignalR`
- Files extend `Hub` or `Hub<T>`
- Files inject `IHubContext<THub>`
- Files use `SendAsync`, `Clients.All`, `Clients.Group`, `Groups.AddToGroupAsync`

## Hubs (→ endpoint facts)

A SignalR hub is a real-time endpoint. Extract the hub class and its client-callable methods:

`public class ScoreHub : Hub` with methods =>
`{"kind":"endpoint","method":"WS","target":"/hubs/ScoreHub"}`

Each public method on the hub is a callable endpoint:
`public async Task WatchFighter(string fighterId)` =>
`{"kind":"endpoint","method":"WS","target":"/hubs/ScoreHub/WatchFighter"}`

## Hub context injection (→ injects facts)

When a service injects `IHubContext<ScoreHub>`:
`private readonly IHubContext<ScoreHub> _hubContext;` =>
`{"kind":"injects","to":"ScoreHub"}`

## Broadcasting (→ produces facts)

When a service calls `SendAsync` on hub clients to push events:
`_hubContext.Clients.All.SendAsync("ScoreUpdated", data)` =>
`{"kind":"produces","to":"ScoreUpdated","method":"SendAsync"}`

Group-targeted sends:
`_hubContext.Clients.Group("leaderboard").SendAsync("LeaderboardChanged", data)` =>
`{"kind":"produces","to":"LeaderboardChanged","method":"SendAsync"}`

## Group management

Hub methods that manage groups indicate subscription patterns but don't need separate facts —
they are part of the hub's endpoint behavior:
`Groups.AddToGroupAsync(Context.ConnectionId, "fighter-{id}")` — no separate fact needed,
captured by the hub endpoint.

## Dependency injection (→ injects facts)

Constructor injection in hubs:
`public ScoreHub(IScoreService scoreService)` =>
`{"kind":"injects","to":"IScoreService"}`

## Hierarchy — Parent Assignment

- Hub classes → parent is the project/assembly
- Services using IHubContext → already have their own parent from their module/project

## Do NOT extract

- Hub pipeline filters/middleware
- Connection lifecycle events (OnConnectedAsync, OnDisconnectedAsync) unless they have business logic
- SignalR configuration (AddSignalR, MapHub)
- Test hubs
