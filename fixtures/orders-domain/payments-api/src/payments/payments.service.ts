import { Injectable } from '@nestjs/common';

@Injectable()
export class PaymentsService {
  processCharge(orderId: string, amount: number) {
    return { orderId, amount, status: 'charged', transactionId: 'txn_123' };
  }

  processRefund(orderId: string, amount: number) {
    return { orderId, amount, status: 'refunded', transactionId: 'txn_456' };
  }
}
