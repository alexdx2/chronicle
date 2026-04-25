import { Injectable } from '@nestjs/common';
import { EventPattern } from '@nestjs/microservices';
import { SpectatorService } from './spectator.service';

@Injectable()
export class BattleResultConsumer {
  constructor(private readonly spectatorService: SpectatorService) {}

  @EventPattern('battle-results')
  handleBattleResult(event: any) {
    console.log('Battle result received:', event);
    this.spectatorService.recordBattle(event);
  }
}
