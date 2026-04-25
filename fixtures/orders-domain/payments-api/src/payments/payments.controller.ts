import { Controller, Post, Body } from '@nestjs/common';
import { PaymentsService } from './payments.service';

@Controller('payments')
export class PaymentsController {
  constructor(private readonly paymentsService: PaymentsService) {}

  @Post('charge')
  charge(@Body() body: { orderId: string; amount: number }) {
    return this.paymentsService.processCharge(body.orderId, body.amount);
  }

  @Post('refund')
  refund(@Body() body: { orderId: string; amount: number }) {
    return this.paymentsService.processRefund(body.orderId, body.amount);
  }

  @Post('status')
  status(@Body() body: { orderId: string }) {
    return this.paymentsService.getStatus(body.orderId);
  }
}
