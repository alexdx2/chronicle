import Redis from 'ioredis';
import { createClient } from 'redis';

async function run(redis: Redis, c: any, sub: Redis) {
  await redis.xadd('stream-a', '*', 'k', 'v');
  await c.xAdd('stream-b', '*', { k: 'v' });
  await redis.xreadgroup('GROUP', g, me, 'COUNT', 10, 'STREAMS', 'stream-c', '>');
  await redis.xread('BLOCK', 100, 'STREAMS', 'stream-d', 'stream-e', '$', '$');
  await c.xReadGroup(g, me, { key: 'stream-f', id: '>' });
  await c.xRead([{ key: 'stream-g', id: '0' }]);
  await redis.publish('chan-a', 'x');
  sub.subscribe('chan-b', 'chan-c', (err) => {});
  sub.psubscribe('events:*');
  await c.pSubscribe('chan-d', listener);
  // await redis.publish('chan-commented', 'x');
  await redis.publish(chanVar, 'x');
  await redis.xadd(`stream-${n}`, '*', 'k', 'v');
  await redis.xread('STREAMS', keys, '$');
  log.info("redis.publish('chan-logged', x) and redis.xadd('stream-logged')");
}
