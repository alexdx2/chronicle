import { connect, NatsConnection } from 'nats';

export async function run(nc: NatsConnection) {
  nc.publish('js.out', data);
  const sub = nc.subscribe("js.in");
  const js = nc.jetstream();
  await js.publish('js.durable', data);
  await js.subscribe(`js.tmpl`);
  const jsm = await nc.jetstreamManager();
  await jsm.streams.add({ name: 'JSSTREAM', subjects: ['js.>'] });
  // nc.publish('js.commented', data);
  nc.publish(`js.${id}`, data);
  nc.publish(subj, data);
  logger.info("subscribe nc.subscribe('js.logged')");
  events.subscribe((x) => x);
}
