using Microsoft.EntityFrameworkCore;
using Microsoft.AspNetCore.SignalR;
using ScoreboardApi.Data;
using ScoreboardApi.Models;
using ScoreboardApi.Hubs;

namespace ScoreboardApi.Services;

public interface IScoreService
{
    Task<List<Score>> GetLeaderboardAsync();
    Task<Score?> GetFighterScoreAsync(string fighterId);
    Task<List<BattleRecord>> GetBattleHistoryAsync(string fighterId);
    Task RecordBattleResultAsync(string battleId, string winnerId, string loserId, string arenaId, int rounds);
}

public class ScoreService : IScoreService
{
    private readonly ScoreboardDbContext _context;
    private readonly IHubContext<ScoreHub> _hubContext;

    public ScoreService(ScoreboardDbContext context, IHubContext<ScoreHub> hubContext)
    {
        _context = context;
        _hubContext = hubContext;
    }

    public async Task<List<Score>> GetLeaderboardAsync()
    {
        return await _context.Scores
            .OrderByDescending(s => s.Wins)
            .ThenBy(s => s.Losses)
            .ToListAsync();
    }

    public async Task<Score?> GetFighterScoreAsync(string fighterId)
    {
        return await _context.Scores
            .FirstOrDefaultAsync(s => s.FighterId == fighterId);
    }

    public async Task<List<BattleRecord>> GetBattleHistoryAsync(string fighterId)
    {
        return await _context.BattleRecords
            .Where(b => b.WinnerId == fighterId || b.LoserId == fighterId)
            .OrderByDescending(b => b.PlayedAt)
            .ToListAsync();
    }

    public async Task RecordBattleResultAsync(string battleId, string winnerId, string loserId, string arenaId, int rounds)
    {
        var winnerScore = await GetOrCreateScore(winnerId);
        winnerScore.Wins++;
        winnerScore.TotalBattles++;
        winnerScore.LastBattleAt = DateTime.UtcNow;

        var loserScore = await GetOrCreateScore(loserId);
        loserScore.Losses++;
        loserScore.TotalBattles++;
        loserScore.LastBattleAt = DateTime.UtcNow;

        var record = new BattleRecord
        {
            BattleId = battleId,
            WinnerId = winnerId,
            LoserId = loserId,
            ArenaId = arenaId,
            RoundsPlayed = rounds,
            PlayedAt = DateTime.UtcNow,
            ScoreId = winnerScore.Id
        };

        _context.BattleRecords.Add(record);
        await _context.SaveChangesAsync();

        await _hubContext.Clients.All.SendAsync("ScoreUpdated", new
        {
            Winner = winnerScore,
            Loser = loserScore,
            BattleId = battleId
        });
    }

    private async Task<Score> GetOrCreateScore(string fighterId)
    {
        var score = await _context.Scores.FirstOrDefaultAsync(s => s.FighterId == fighterId);
        if (score == null)
        {
            score = new Score { FighterId = fighterId, FighterName = fighterId };
            _context.Scores.Add(score);
            await _context.SaveChangesAsync();
        }
        return score;
    }
}
