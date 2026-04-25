import { Injectable, CanActivate, ExecutionContext } from '@nestjs/common';

@Injectable()
export class BattleGuard implements CanActivate {
  canActivate(context: ExecutionContext): boolean {
    const request = context.switchToHttp().getRequest();
    // Only allow attacks during daytime (Tom sleeps at night)
    const hour = new Date().getHours();
    return hour >= 6 && hour < 22;
  }
}
