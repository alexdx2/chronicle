import { Injectable } from '@nestjs/common';
import { PrismaService } from '../prisma/prisma.service';
import { TomClient } from './tom.client';
import { JerryClient } from './jerry.client';
import { BattleResultProducer } from './battle-result.producer';

@Injectable()
export class ArenaService {
  constructor(
    private readonly prisma: PrismaService,
    private readonly tomClient: TomClient,
    private readonly jerryClient: JerryClient,
    private readonly battleResultProducer: BattleResultProducer,
  ) {}

  async tomAttacksJerry(weaponType: string) {
    const tom = await this.tomClient.getStatus();
    const jerry = await this.jerryClient.getStatus();

    // Check if Jerry has active traps
    const traps = await this.jerryClient.getTraps();
    const activeTrap = traps.find((t: any) => t.isSet);

    let result: string;
    let damage = 0;

    if (activeTrap) {
      result = 'TRAP_TRIGGERED';
      damage = 0; // Tom falls into trap
    } else if (jerry.speed > tom.speed + 20) {
      result = 'JERRY_DODGES';
      damage = 0;
    } else {
      result = 'TOM_HITS';
      damage = 25;
    }

    const event = await this.prisma.battleEvent.create({
      data: {
        tomId: tom.id || 'tom',
        jerryId: jerry.id || 'jerry',
        weapon: weaponType,
        trap: activeTrap?.name || null,
        result: result as any,
        damage,
      },
    });

    await this.battleResultProducer.publish(event);
    return { ...event, tom: tom.name, jerry: jerry.name };
  }

  async jerrySetsTrap(trapType: string) {
    return { message: `Jerry sets a ${trapType} trap!`, trapType };
  }

  getHistory() {
    return this.prisma.battleEvent.findMany({ orderBy: { createdAt: 'desc' }, take: 20 });
  }

  async getScore() {
    const tomWins = await this.prisma.battleEvent.count({ where: { result: 'TOM_HITS' } });
    const jerryWins = await this.prisma.battleEvent.count({
      where: { result: { in: ['JERRY_DODGES', 'TRAP_TRIGGERED'] } },
    });
    return { tom: tomWins, jerry: jerryWins };
  }
}
