import * as sqs from 'aws-cdk-lib/aws-sqs';

new sqs.Queue(this, 'Orders', {
  queueName: 'cdk-orders',
});
new Topic(this, 'T', { topicName: 'cdk-topic' });
new sqs.Queue(this, 'NoName', { visibilityTimeout: Duration.seconds(3) });
new sqs.Queue(this, 'Cat', {
  queueName: 'q-' + env,
});
