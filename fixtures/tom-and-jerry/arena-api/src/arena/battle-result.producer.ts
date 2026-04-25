import { Injectable } from '@nestjs/common';

const TOPIC = 'battle-results';

@Injectable()
export class BattleResultProducer {
  async publish(event: any) {
    // In production: this.kafka.producer.send({ topic: TOPIC, messages: [...] })
    console.log(`Publishing to ${TOPIC}:`, event);
  }
}
