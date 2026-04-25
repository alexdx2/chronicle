import { Injectable } from '@nestjs/common';
import { Cron, CronExpression } from '@nestjs/schedule';
import { SpectatorService } from './spectator.service';

@Injectable()
export class CleanupTask {
  constructor(private readonly spectatorService: SpectatorService) {}

  @Cron(CronExpression.EVERY_HOUR)
  handleCleanup() {
    console.log('Cleaning up old battle records...');
    // In production: delete records older than 24h
  }

  @Cron('0 0 * * *') // midnight
  handleDailyReport() {
    const leaderboard = this.spectatorService.getLeaderboard();
    console.log('Daily Tom & Jerry report:', leaderboard);
  }
}
