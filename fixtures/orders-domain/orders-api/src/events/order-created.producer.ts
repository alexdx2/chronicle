import { Injectable } from '@nestjs/common';

const TOPIC = 'order-created';

@Injectable()
export class OrderCreatedProducer {
  async publish(order: any) {
    // In production: this.kafka.producer.send({ topic: TOPIC, messages: [...] })
    console.log(`Publishing to ${TOPIC}:`, order);
  }
}
