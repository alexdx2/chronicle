using Confluent.Kafka;
using System.Text.Json;

namespace ScoreboardApi.Services;

/// <summary>
/// Consumes battle-result events from Kafka (same topic as spectators-api).
/// Topic: battle-results
/// </summary>
public class BattleResultConsumer : BackgroundService
{
    private readonly IServiceScopeFactory _scopeFactory;
    private readonly ILogger<BattleResultConsumer> _logger;

    public BattleResultConsumer(IServiceScopeFactory scopeFactory, ILogger<BattleResultConsumer> logger)
    {
        _scopeFactory = scopeFactory;
        _logger = logger;
    }

    protected override async Task ExecuteAsync(CancellationToken stoppingToken)
    {
        var config = new ConsumerConfig
        {
            BootstrapServers = "kafka:9092",
            GroupId = "scoreboard-consumer",
            AutoOffsetReset = AutoOffsetReset.Earliest
        };

        using var consumer = new ConsumerBuilder<string, string>(config).Build();
        consumer.Subscribe("battle-results");

        while (!stoppingToken.IsCancellationRequested)
        {
            try
            {
                var result = consumer.Consume(stoppingToken);
                var battleResult = JsonSerializer.Deserialize<BattleResultEvent>(result.Message.Value);

                if (battleResult != null)
                {
                    using var scope = _scopeFactory.CreateScope();
                    var scoreService = scope.ServiceProvider.GetRequiredService<IScoreService>();

                    await scoreService.RecordBattleResultAsync(
                        battleResult.BattleId,
                        battleResult.WinnerId,
                        battleResult.LoserId,
                        battleResult.ArenaId,
                        battleResult.Rounds
                    );

                    _logger.LogInformation("Recorded battle result: {BattleId}", battleResult.BattleId);
                }
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch (Exception ex)
            {
                _logger.LogError(ex, "Error consuming battle result");
                await Task.Delay(1000, stoppingToken);
            }
        }
    }
}

public record BattleResultEvent(
    string BattleId,
    string WinnerId,
    string LoserId,
    string ArenaId,
    int Rounds
);
