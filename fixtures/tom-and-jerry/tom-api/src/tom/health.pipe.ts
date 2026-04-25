import { PipeTransform, Injectable, BadRequestException } from '@nestjs/common';

@Injectable()
export class HealthCheckPipe implements PipeTransform {
  transform(value: any) {
    if (value.health !== undefined && (value.health < 0 || value.health > 9)) {
      throw new BadRequestException('Health must be between 0 and 9 (Tom has 9 lives)');
    }
    return value;
  }
}
