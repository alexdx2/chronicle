import { Module } from '@nestjs/common';
import { JerryController } from './jerry.controller';
import { JerryService } from './jerry.service';
import { PrismaService } from '../prisma/prisma.service';

@Module({
  controllers: [JerryController],
  providers: [JerryService, PrismaService],
})
export class JerryModule {}
