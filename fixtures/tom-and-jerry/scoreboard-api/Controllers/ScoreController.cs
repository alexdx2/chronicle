using Microsoft.AspNetCore.Mvc;
using ScoreboardApi.Services;
using ScoreboardApi.Models;

namespace ScoreboardApi.Controllers;

[ApiController]
[Route("api/[controller]")]
public class ScoreController : ControllerBase
{
    private readonly IScoreService _scoreService;

    public ScoreController(IScoreService scoreService)
    {
        _scoreService = scoreService;
    }

    [HttpGet]
    public async Task<ActionResult<List<Score>>> GetLeaderboard()
    {
        var scores = await _scoreService.GetLeaderboardAsync();
        return Ok(scores);
    }

    [HttpGet("{fighterId}")]
    public async Task<ActionResult<Score>> GetFighterScore(string fighterId)
    {
        var score = await _scoreService.GetFighterScoreAsync(fighterId);
        if (score == null) return NotFound();
        return Ok(score);
    }

    [HttpGet("{fighterId}/history")]
    public async Task<ActionResult<List<BattleRecord>>> GetBattleHistory(string fighterId)
    {
        var records = await _scoreService.GetBattleHistoryAsync(fighterId);
        return Ok(records);
    }
}
