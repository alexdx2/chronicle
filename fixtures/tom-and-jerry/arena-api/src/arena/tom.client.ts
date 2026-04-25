import { Injectable, HttpException } from '@nestjs/common';

const TOM_API_URL = process.env.TOM_API_URL || 'http://tom-api:3001';

@Injectable()
export class TomClient {
  async getStatus() {
    const response = await fetch(`${TOM_API_URL}/tom/status`);
    if (!response.ok) throw new HttpException('Tom unavailable', response.status);
    return response.json();
  }
}
