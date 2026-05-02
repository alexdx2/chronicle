import { Injectable } from '@nestjs/common';

@Injectable()
export class PaymentService {
  async charge(amount: number) {
    return { status: 'paid', amount };
  }
}
