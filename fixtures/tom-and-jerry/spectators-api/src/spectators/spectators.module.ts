import { Module } from '@nestjs/common';
import { StatsController } from './stats.controller';
import { SpectatorService } from './spectator.service';
import { BattleResultConsumer } from './battle-result.consumer';
import { CleanupTask } from './cleanup.task';

@Module({
  controllers: [StatsController],
  providers: [SpectatorService, BattleResultConsumer, CleanupTask],
})
export class SpectatorsModule {}
