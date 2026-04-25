import { Controller, Get, Post, Body, UseInterceptors } from '@nestjs/common';
import { JerryService } from './jerry.service';
import { TrapInterceptor } from './trap.interceptor';

@Controller('jerry')
@UseInterceptors(TrapInterceptor)
export class JerryController {
  constructor(private readonly jerryService: JerryService) {}

  @Get('status')
  getStatus() {
    return this.jerryService.getStatus();
  }

  @Get('traps')
  getTraps() {
    return this.jerryService.getTraps();
  }

  @Post('set-trap')
  setTrap(@Body() body: { trapType: string }) {
    return this.jerryService.setTrap(body.trapType);
  }
}
