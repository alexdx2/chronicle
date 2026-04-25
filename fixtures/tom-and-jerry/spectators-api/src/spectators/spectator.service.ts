import { Injectable } from '@nestjs/common';

const TOM_API_URL = process.env.TOM_API_URL || 'http://tom-api:3001';
const JERRY_API_URL = process.env.JERRY_API_URL || 'http://jerry-api:3002';

@Injectable()
export class SpectatorService {
  private battles: any[] = [];

  recordBattle(event: any) {
    this.battles.push(event);
  }

  getLeaderboard() {
    const tomWins = this.battles.filter(b => b.result === 'TOM_HITS').length;
    const jerryWins = this.battles.filter(b => ['JERRY_DODGES', 'TRAP_TRIGGERED'].includes(b.result)).length;
    return { tom: tomWins, jerry: jerryWins, total: this.battles.length };
  }

  getRecentBattles() {
    return this.battles.slice(-10);
  }

  async getTomName() {
    const r = await fetch(`${TOM_API_URL}/tom/status`);
    return r.ok ? (await r.json()).name : 'Tom';
  }

  async getJerryName() {
    const r = await fetch(`${JERRY_API_URL}/jerry/status`);
    return r.ok ? (await r.json()).name : 'Jerry';
  }
}
