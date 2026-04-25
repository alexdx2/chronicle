import { Injectable } from '@nestjs/common';
import { PrismaService } from '../prisma/prisma.service';

@Injectable()
export class TomService {
  constructor(private readonly prisma: PrismaService) {}

  async getStatus() {
    const tom = await this.prisma.cat.findFirst({
      where: { name: 'Tom' },
      include: { weapons: true },
    });
    return tom || { name: 'Tom', health: 9, mood: 'HUNTING', weapons: [] };
  }

  getWeapons() {
    return this.prisma.catWeapon.findMany();
  }

  async arm(weaponType: string) {
    const tom = await this.prisma.cat.findFirst({ where: { name: 'Tom' } });
    if (!tom) throw new Error('Tom not found');
    return this.prisma.catWeapon.create({
      data: { name: weaponType, damage: 25, type: weaponType as any, ownerId: tom.id },
    });
  }
}
