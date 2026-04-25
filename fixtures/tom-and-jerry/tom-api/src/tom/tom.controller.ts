import { Controller, Get, Post, Param, Body } from '@nestjs/common';
import { TomService } from './tom.service';

@Controller('tom')
export class TomController {
  constructor(private readonly tomService: TomService) {}

  @Get('status')
  getStatus() {
    return this.tomService.getStatus();
  }

  @Get('weapons')
  getWeapons() {
    return this.tomService.getWeapons();
  }

  @Post('arm')
  arm(@Body() body: { weaponType: string }) {
    return this.tomService.arm(body.weaponType);
  }
}
