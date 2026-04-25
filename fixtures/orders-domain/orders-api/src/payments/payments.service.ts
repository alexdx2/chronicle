import { Injectable, HttpException } from '@nestjs/common';

const PAYMENTS_API_URL = process.env.PAYMENTS_API_URL || 'http://payments-api:3001';

@Injectable()
export class PaymentsService {
  async charge(orderId: string, amount: number) {
    const response = await fetch(`${PAYMENTS_API_URL}/payments/charge`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ orderId, amount }),
    });
    if (!response.ok) {
      throw new HttpException('Payment failed', response.status);
    }
    return response.json();
  }
}
