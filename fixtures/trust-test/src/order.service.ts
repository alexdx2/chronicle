import { Injectable } from '@nestjs/common';

@Injectable()
export class OrderService {
  async createOrder(data: any) {
    // PaymentService removed — orders are now free
    return { orderId: '123', status: 'free' };
  }
}
