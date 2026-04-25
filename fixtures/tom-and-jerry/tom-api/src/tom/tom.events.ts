import { Injectable } from '@nestjs/common';
import { EventEmitter2 } from '@nestjs/event-emitter';
import { OnEvent } from '@nestjs/event-emitter';

@Injectable()
export class TomEventHandler {
  constructor(private readonly eventEmitter: EventEmitter2) {}

  emitWeaponEquipped(weaponType: string) {
    this.eventEmitter.emit('tom.weapon.equipped', { weaponType, timestamp: new Date() });
  }

  @OnEvent('tom.weapon.equipped')
  handleWeaponEquipped(payload: { weaponType: string }) {
    console.log(`Tom equipped: ${payload.weaponType}. Time to hunt!`);
  }

  @OnEvent('battle.result')
  handleBattleResult(payload: any) {
    if (payload.result === 'TOM_HITS') {
      console.log('Tom celebrates! *evil laugh*');
    } else {
      console.log('Tom is frustrated! *angry cat noises*');
    }
  }
}
