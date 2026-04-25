import { Injectable } from '@nestjs/common';
import { PaymentsService } from '../payments/payments.service';
import { OrderCreatedProducer } from '../events/order-created.producer';

@Injectable()
export class OrdersService {
  constructor(
    private readonly paymentsService: PaymentsService,
    private readonly orderCreatedProducer: OrderCreatedProducer,
  ) {}

  findAll() {
    return [];
  }

  findOne(id: string) {
    return { id };
  }

  async create(data: any) {
    const order = { id: '1', ...data, status: 'created' };
    await this.orderCreatedProducer.publish(order);
    return order;
  }

  async capture(id: string) {
    const charge = await this.paymentsService.charge(id, 100);
    return { id, status: 'captured', charge };
  }

  async cancel(id: string) {
    return { id, status: 'cancelled' };
  }

  async refund(id: string, amount: number) {
    return this.paymentsService.refund(id, amount);
  }
}
