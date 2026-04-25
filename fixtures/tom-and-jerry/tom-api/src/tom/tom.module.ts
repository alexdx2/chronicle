import { Module, MiddlewareConsumer, NestModule } from '@nestjs/common';
import { TomController } from './tom.controller';
import { TomService } from './tom.service';
import { PrismaService } from '../prisma/prisma.service';
import { LoggingMiddleware } from './logging.middleware';

@Module({
  controllers: [TomController],
  providers: [TomService, PrismaService],
})
export class TomModule implements NestModule {
  configure(consumer: MiddlewareConsumer) {
    consumer.apply(LoggingMiddleware).forRoutes(TomController);
  }
}
