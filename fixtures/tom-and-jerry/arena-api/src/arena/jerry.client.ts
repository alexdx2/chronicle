import { Injectable, HttpException } from '@nestjs/common';

const JERRY_API_URL = process.env.JERRY_API_URL || 'http://jerry-api:3002';

@Injectable()
export class JerryClient {
  async getStatus() {
    const response = await fetch(`${JERRY_API_URL}/jerry/status`);
    if (!response.ok) throw new HttpException('Jerry unavailable', response.status);
    return response.json();
  }

  async getTraps() {
    const response = await fetch(`${JERRY_API_URL}/jerry/traps`);
    if (!response.ok) return [];
    return response.json();
  }
}
