import { Injectable } from '@nestjs/common';

const REDIS_URL = process.env.REDIS_URL || 'redis://localhost:6379';

@Injectable()
export class CacheService {
  private cache = new Map<string, any>();

  async get(key: string): Promise<any> {
    // In production: redis.get(key)
    return this.cache.get(key);
  }

  async set(key: string, value: any, ttlSeconds: number = 60): Promise<void> {
    // In production: redis.setex(key, ttlSeconds, JSON.stringify(value))
    this.cache.set(key, value);
  }

  async invalidate(pattern: string): Promise<void> {
    // In production: redis.keys(pattern).then(keys => redis.del(...keys))
    for (const key of this.cache.keys()) {
      if (key.includes(pattern)) this.cache.delete(key);
    }
  }
}
