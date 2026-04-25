import { Controller, Get } from '@nestjs/common';
import { SpectatorService } from './spectator.service';

@Controller('stats')
export class StatsController {
  constructor(private readonly spectatorService: SpectatorService) {}

  @Get('leaderboard')
  getLeaderboard() {
    return this.spectatorService.getLeaderboard();
  }

  @Get('recent')
  getRecentBattles() {
    return this.spectatorService.getRecentBattles();
  }
}
