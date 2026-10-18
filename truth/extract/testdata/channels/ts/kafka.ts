await consumer.subscribe({ topic: 'orders-created', fromBeginning: true })
await consumer.subscribe({ topics: ['a-topic', "b-topic"] })
await consumer.subscribe({ topic: config.topic })
await producer.send({
  topic: 'payments-done',
  messages: [],
})
await producer.send({ topic: `evt-${env}`, messages: [] })
@EventPattern('user.created')
@MessagePattern("user.get")
handle() {}
// await producer.send({ topic: 'commented', messages: [] })
const r = await sqs.send(new SendMessageCommand({ QueueUrl: 'https://sqs.us-east-1.amazonaws.com/111/js-out', MessageBody: 'x' }))
// ------
// ------
// ------
// ------
// ------
// ------
// ------
// ------
const m = await sqs.send(new ReceiveMessageCommand({ QueueUrl: 'https://sqs.us-east-1.amazonaws.com/111/js-in' }))
await producer.send({ topic: 'orders-' + env, messages: [] })
await consumer.subscribe({ topics: ['a-' + env, "kept-ts"] })
