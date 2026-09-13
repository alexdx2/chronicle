import { BillingService } from './billing.service';

@Controller('billing')
export class BillingController {
  constructor(private readonly billing: BillingService) {}

  @Get('invoices')
  list() {
    return this.billing.listInvoices();
  }

  @Post('invoices')
  create(body: unknown) {
    return this.billing.createInvoice(body);
  }

  @Delete('invoices/:id')
  remove(id: string) {
    return this.billing.removeInvoice(id);
  }
}
