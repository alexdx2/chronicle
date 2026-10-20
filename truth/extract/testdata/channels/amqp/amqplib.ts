import amqp from 'amqplib';
import Redis from 'ioredis';

const redis = new Redis();

export async function setup(conn: amqp.Connection) {
  const channel = await conn.createChannel();
  await channel.assertExchange('orders.events', 'topic', { durable: true });
  await channel.assertQueue('billing.orders', { durable: true });
  await channel.bindQueue('billing.orders', 'orders.events', 'order.*');
  await channel.consume('billing.orders', (msg) => {
    channel.ack(msg!);
  });
  await channel.consume('plain.queue', () => {});
  channel.publish('audit.events', 'k', Buffer.from('x'));
  channel.publish('', 'direct.queue', Buffer.from('x'));
  channel.sendToQueue('tasks', Buffer.from('x'));
  // channel.sendToQueue('commented-out', Buffer.from('x'));
  channel.sendToQueue(config.dynamicQueue, Buffer.from('x'));
  channel.publish(`${prefix}.events`, 'k', Buffer.from('x'));
  channel.assertQueue('');
  log.info("channel.sendToQueue('only-a-log-line')");
  redis.publish('redis-channel', 'msg');
}
