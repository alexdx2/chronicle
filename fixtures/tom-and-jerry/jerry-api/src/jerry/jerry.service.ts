import { Injectable } from '@nestjs/common';
import { PrismaService } from '../prisma/prisma.service';

@Injectable()
export class JerryService {
  constructor(private readonly prisma: PrismaService) {}

  async getStatus() {
    const jerry = await this.prisma.mouse.findFirst({
      where: { name: 'Jerry' },
      include: { traps: true },
    });
    return jerry || { name: 'Jerry', health: 3, speed: 90, clever: true, traps: [] };
  }

  getTraps() {
    return this.prisma.trap.findMany({ where: { isSet: true } });
  }

  async setTrap(trapType: string) {
    const jerry = await this.prisma.mouse.findFirst({ where: { name: 'Jerry' } });
    if (!jerry) throw new Error('Jerry not found');
    return this.prisma.trap.create({
      data: { name: trapType, effect: trapType as any, ownerId: jerry.id, isSet: true },
    });
  }
}
