import { Injectable } from '@nestjs/common';
import { InjectQueue, Process, Processor } from '@nestjs/bull';
import { Queue, Job } from 'bull';
import { ArenaService } from './arena.service';

const QUEUE_NAME = 'battle-queue';

@Injectable()
export class BattleQueueProducer {
  constructor(@InjectQueue(QUEUE_NAME) private readonly battleQueue: Queue) {}

  async addAttack(weaponType: string) {
    return this.battleQueue.add('attack', { weaponType }, { delay: 1000 });
  }

  async addCombo(weapons: string[]) {
    return this.battleQueue.add('combo', { weapons }, { priority: 1 });
  }
}

@Processor(QUEUE_NAME)
export class BattleQueueConsumer {
  constructor(private readonly arenaService: ArenaService) {}

  @Process('attack')
  async handleAttack(job: Job<{ weaponType: string }>) {
    console.log(`Processing attack job #${job.id}: ${job.data.weaponType}`);
    return this.arenaService.tomAttacksJerry(job.data.weaponType);
  }

  @Process('combo')
  async handleCombo(job: Job<{ weapons: string[] }>) {
    for (const weapon of job.data.weapons) {
      await this.arenaService.tomAttacksJerry(weapon);
    }
  }
}
