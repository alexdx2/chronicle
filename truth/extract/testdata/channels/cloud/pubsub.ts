import { PubSub } from '@google-cloud/pubsub';

const pubsub = new PubSub();
const out = pubsub.topic('ps-js-out');
const sub = pubsub.subscription('ps-js-sub');
await out.publishMessage({ json: {} });
sub.on('message', handle);
sub.on('error', handle);
pubsub.topic('ps-js-inline').publishJSON({ a: 1 });
pubsub
  .subscription('projects/p/subscriptions/ps-js-full')
  .on('message', handle);
// pubsub.topic('ps-js-old').publish(Buffer.from('x'));
pubsub.topic(name).publish(buf);
out.on('message', handle);
