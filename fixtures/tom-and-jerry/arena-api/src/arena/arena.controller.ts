import { Controller, Post, Get, Body, UseGuards } from '@nestjs/common';
import { ArenaService } from './arena.service';
import { BattleGuard } from './battle.guard';

@Controller('arena')
export class ArenaController {
  constructor(private readonly arenaService: ArenaService) {}

  @Post('attack')
  @UseGuards(BattleGuard)
  attack(@Body() body: { weaponType: string }) {
    return this.arenaService.tomAttacksJerry(body.weaponType);
  }

  @Post('trap')
  setTrap(@Body() body: { trapType: string }) {
    return this.arenaService.jerrySetsTrap(body.trapType);
  }

  @Get('history')
  getHistory() {
    return this.arenaService.getHistory();
  }

  @Get('score')
  getScore() {
    return this.arenaService.getScore();
  }
}
