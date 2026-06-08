namespace ScoreboardApi.Models;

public class Score
{
    public int Id { get; set; }
    public string FighterId { get; set; } = string.Empty;
    public string FighterName { get; set; } = string.Empty;
    public int Wins { get; set; }
    public int Losses { get; set; }
    public int TotalBattles { get; set; }
    public DateTime LastBattleAt { get; set; }

    public List<BattleRecord> BattleRecords { get; set; } = new();
}

public class BattleRecord
{
    public int Id { get; set; }
    public string BattleId { get; set; } = string.Empty;
    public string WinnerId { get; set; } = string.Empty;
    public string LoserId { get; set; } = string.Empty;
    public string ArenaId { get; set; } = string.Empty;
    public int RoundsPlayed { get; set; }
    public DateTime PlayedAt { get; set; }

    public int ScoreId { get; set; }
    public Score Score { get; set; } = null!;
}
