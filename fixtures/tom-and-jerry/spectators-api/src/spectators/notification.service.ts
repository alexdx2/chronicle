import { Injectable } from '@nestjs/common';

const NOTIFICATION_SERVICE_URL = process.env.NOTIFICATION_SERVICE_URL || 'http://notifications:3005';

@Injectable()
export class NotificationService {
  async notifySpectators(event: any) {
    // External service call — push notification to mobile spectators
    try {
      await fetch(`${NOTIFICATION_SERVICE_URL}/push`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          title: `Battle Update: ${event.result}`,
          body: `Tom vs Jerry — Round ${event.round}`,
        }),
      });
    } catch (e) {
      console.error('Failed to notify spectators:', e);
    }
  }

  async sendDailyDigest() {
    await fetch(`${NOTIFICATION_SERVICE_URL}/email/digest`, {
      method: 'POST',
    });
  }
}
