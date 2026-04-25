import { Module } from '@nestjs/common';
import { ArenaController } from './arena.controller';
import { ArenaService } from './arena.service';
import { TomClient } from './tom.client';
import { JerryClient } from './jerry.client';
import { BattleResultProducer } from './battle-result.producer';
import { BattleGateway } from './battle.gateway';
import { BattleGuard } from './battle.guard';
import { PrismaService } from '../prisma/prisma.service';

@Module({
  controllers: [ArenaController],
  providers: [ArenaService, TomClient, JerryClient, BattleResultProducer, BattleGateway, BattleGuard, PrismaService],
})
export class ArenaModule {}
