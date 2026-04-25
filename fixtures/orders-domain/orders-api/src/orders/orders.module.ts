import { Module } from '@nestjs/common';
import { OrdersController } from './orders.controller';
import { OrdersService } from './orders.service';
import { PaymentsService } from '../payments/payments.service';
import { OrderCreatedProducer } from '../events/order-created.producer';

@Module({
  controllers: [OrdersController],
  providers: [OrdersService, PaymentsService, OrderCreatedProducer],
})
export class OrdersModule {}
