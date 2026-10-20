import * as cdk from 'aws-cdk-lib';
import * as events from 'aws-cdk-lib/aws-events';

const a = new events.EventBus(this, 'A', { eventBusName: 'bus-a' });
const b = new events.EventBus(this, 'B', {
  archive: { retention: cdk.Duration.days(1) },
  eventBusName: 'bus-b',
});
const c = new events.EventBus(this, 'C');
const d = new events.EventBus(this, 'D', { eventBusName: props.name });
const e = events.EventBus.fromEventBusName(this, 'E', 'bus-existing');
// const f = new events.EventBus(this, 'F', { eventBusName: 'bus-old' });
