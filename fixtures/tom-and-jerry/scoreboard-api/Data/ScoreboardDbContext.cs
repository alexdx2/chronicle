using Microsoft.EntityFrameworkCore;
using ScoreboardApi.Models;

namespace ScoreboardApi.Data;

public class ScoreboardDbContext : DbContext
{
    public ScoreboardDbContext(DbContextOptions<ScoreboardDbContext> options) : base(options) { }

    public DbSet<Score> Scores { get; set; }
    public DbSet<BattleRecord> BattleRecords { get; set; }

    protected override void OnModelCreating(ModelBuilder modelBuilder)
    {
        modelBuilder.Entity<Score>()
            .HasMany(s => s.BattleRecords)
            .WithOne(b => b.Score)
            .HasForeignKey(b => b.ScoreId);

        modelBuilder.Entity<Score>()
            .HasIndex(s => s.FighterId)
            .IsUnique();
    }
}
