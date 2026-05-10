import { Injectable, Logger } from '@nestjs/common';
import { Expo, ExpoPushMessage, ExpoPushTicket, ExpoPushReceiptId } from 'expo-server-sdk';
import { PrismaService } from '../prisma.service';

export interface NotificationTemplate {
  title: string;
  body: string;
  data?: any;
  sound?: string;
  badge?: number;
  priority?: 'default' | 'normal' | 'high';
  channelId?: string;
}

export interface NotificationPayload {
  userIds: string[];
  template: NotificationTemplate;
  type: 'system' | 'marketing' | 'order' | 'reward';
  merchantId?: string;
}

@Injectable()
export class NotificationService {
  private readonly logger = new Logger(NotificationService.name);
  private expo: Expo;

  constructor(private prisma: PrismaService) {
    this.expo = new Expo({
      accessToken: process.env.EXPO_ACCESS_TOKEN,
      useFcmV1: true,
    });
  }

  async sendNotification(payload: NotificationPayload): Promise<void> {
    const { userIds, template, type, merchantId } = payload;

    try {
      // Get users with push tokens and notification preferences
      const users = await this.prisma.user.findMany({
        where: {
          id: { in: userIds },
          pushToken: { not: null },
          muteNotifications: false,
        },
        include: {
          merchantSettings: merchantId ? {
            where: { merchantId }
          } : false
        }
      });

      if (users.length === 0) {
        this.logger.warn(`No users found with push tokens for notification: ${template.title}`);
        return;
      }

      // Filter users based on notification preferences
      const filteredUsers = users.filter((user: any) => {
        // Check global notification preferences
        if (type === 'marketing' && !user.notificationPromo) {
          return false;
        }
        if (type === 'system' && !user.notificationService) {
          return false;
        }

        // Check merchant-specific preferences if applicable
        if (merchantId && user.merchantSettings?.length > 0) {
          const merchantSettings = user.merchantSettings[0];
          if (type === 'marketing' && !merchantSettings.allowPromo) {
            return false;
          }
          if ((type === 'system' || type === 'order') && !merchantSettings.allowPush) {
            return false;
          }
        }

        return true;
      });

      if (filteredUsers.length === 0) {
        this.logger.warn(`No users with enabled notifications for type: ${type}`);
        return;
      }

      // Create Expo push messages
      const messages: ExpoPushMessage[] = filteredUsers.map((user: any) => ({
        to: user.pushToken!,
        title: template.title,
        body: template.body,
        data: template.data || {},
        sound: template.sound || 'default',
        badge: template.badge,
        priority: template.priority || 'default',
        channelId: template.channelId || type,
      }));

      // Send notifications in chunks
      const chunks = this.expo.chunkPushNotifications(messages);
      const tickets: ExpoPushTicket[] = [];

      for (const chunk of chunks) {
        try {
          const ticketChunk = await this.expo.sendPushNotificationsAsync(chunk);
          tickets.push(...ticketChunk);
        } catch (error) {
          this.logger.error('Error sending push notification chunk:', error);
        }
      }

      // Process tickets for errors
      await this.processTickets(tickets, filteredUsers);

      this.logger.log(`Successfully sent ${filteredUsers.length} notifications: ${template.title}`);
    } catch (error) {
      this.logger.error('Error sending notifications:', error);
      throw error;
    }
  }

  private async processTickets(tickets: ExpoPushTicket[], users: any[]): Promise<void> {
    const receiptIds: ExpoPushReceiptId[] = [];

    for (let i = 0; i < tickets.length; i++) {
      const ticket = tickets[i];
      const user = users[i];

      if (ticket.status === 'error') {
        this.logger.error(`Push notification error for user ${user.id}:`, ticket.message);
        
        // Handle invalid push tokens
        if (ticket.details?.error === 'DeviceNotRegistered') {
          await this.invalidatePushToken(user.id);
        }
      } else if (ticket.status === 'ok') {
        receiptIds.push(ticket.id);
      }
    }

    // Check receipts for delivery status (optional, for monitoring)
    if (receiptIds.length > 0) {
      setTimeout(() => this.checkReceipts(receiptIds), 30000); // Check after 30 seconds
    }
  }

  private async checkReceipts(receiptIds: ExpoPushReceiptId[]): Promise<void> {
    try {
      const receiptIdChunks = this.expo.chunkPushNotificationReceiptIds(receiptIds);
      
      for (const chunk of receiptIdChunks) {
        const receipts = await this.expo.getPushNotificationReceiptsAsync(chunk);
        
        for (const receiptId in receipts) {
          const receipt = receipts[receiptId];
          
          if (receipt.status === 'error') {
            this.logger.error(`Push notification receipt error for ${receiptId}:`, receipt.message);
          }
        }
      }
    } catch (error) {
      this.logger.error('Error checking push notification receipts:', error);
    }
  }

  private async invalidatePushToken(userId: string): Promise<void> {
    try {
      await this.prisma.user.update({
        where: { id: userId },
        data: { pushToken: null }
      });
      this.logger.log(`Invalidated push token for user ${userId}`);
    } catch (error) {
      this.logger.error(`Error invalidating push token for user ${userId}:`, error);
    }
  }

  // Notification templates
  async sendOrderStatusNotification(
    userId: string,
    orderId: string,
    status: any,
    merchantName: string,
    merchantId?: string
  ): Promise<void> {
    const templates: Record<string, any> = {
      PREPARING: {
        title: '🍳 Order is being prepared',
        body: `Your order at ${merchantName} is being prepared`,
        data: { orderId, status: 'PREPARING', type: 'order_status', merchantId },
        channelId: 'system'
      },
      READY_FOR_PICKUP: {
        title: '✅ Order ready for pickup',
        body: `Your order at ${merchantName} is ready for pickup!`,
        data: { orderId, status: 'READY_FOR_PICKUP', type: 'order_status', merchantId },
        channelId: 'system',
        priority: 'high' as const
      },
      CLOSED: {
        title: '🎉 Order delivered',
        body: `Your order from ${merchantName} has been delivered. Enjoy!`,
        data: { orderId, status: 'CLOSED', type: 'order_status', merchantId },
        channelId: 'system'
      },
      CANCELLED: {
        title: '❌ Order cancelled',
        body: `Your order at ${merchantName} has been cancelled`,
        data: { orderId, status: 'CANCELLED', type: 'order_status', merchantId },
        channelId: 'system'
      }
    };

    const template = templates[status];
    if (!template) {
      this.logger.warn(`No notification template for order status: ${status}`);
      return;
    }

    await this.sendNotification({
      userIds: [userId],
      template,
      type: 'order',
      merchantId
    });
  }

  async sendRewardNotification(
    userId: string,
    rewardType: 'points' | 'voucher',
    amount: number,
    merchantName: string,
    merchantId?: string
  ): Promise<void> {
    const templates = {
      points: {
        title: '🎁 Points earned!',
        body: `You earned ${amount} points at ${merchantName}`,
        data: { rewardType, amount, type: 'reward', merchantId },
        channelId: 'system'
      },
      voucher: {
        title: '🎫 New voucher received!',
        body: `You received a new voucher from ${merchantName}`,
        data: { rewardType, type: 'reward', merchantId },
        channelId: 'system'
      }
    };

    await this.sendNotification({
      userIds: [userId],
      template: templates[rewardType],
      type: 'reward',
      merchantId
    });
  }

  async sendMarketingNotification(
    userIds: string[],
    title: string,
    body: string,
    data?: any,
    merchantId?: string
  ): Promise<void> {
    await this.sendNotification({
      userIds,
      template: {
        title,
        body,
        data: { ...data, type: 'marketing' },
        channelId: 'marketing'
      },
      type: 'marketing',
      merchantId
    });
  }

  /**
   * Send notification to merchant staff when order status changes
   * Used to notify waiters/managers when kitchen marks order as READY
   */
  async sendMerchantStaffNotification(
    merchantId: string,
    orderId: string,
    status: string,
    printerInfo: { id: number; name: string },
  ): Promise<void> {
    try {
      // Get merchant staff with roles that should receive order notifications
      // Exclude KITCHEN (they make it ready) and SUPPORT (chat only)
      const staffMembers = await this.prisma.merchantMember.findMany({
        where: {
          merchantId,
          isActive: true,
          role: {
            in: ['OWNER', 'ADMIN', 'MANAGER', 'WAITER'],
          },
        },
        include: {
          crmUser: {
            include: {
              user: true, // CRMUser -> User for pushToken
            },
          },
        },
      });

      // Get CRM staff users with valid push tokens
      const crmUsersWithTokens = staffMembers
        .filter((member: any) => member.crmUser?.user?.pushToken)
        .map((member: any) => member.crmUser.user);

      // Get mobile merchant managers with push tokens
      const merchantWithManagers = await this.prisma.merchant.findUnique({
        where: { id: merchantId },
        include: {
          managers: {
            select: { id: true, pushToken: true },
          },
        },
      });

      const mobileManagersWithTokens = (merchantWithManagers?.managers ?? [])
        .filter((m: any) => m.pushToken);

      // Combine and deduplicate by push token
      const seenTokens = new Set<string>();
      const usersWithTokens: Array<{ id: string; pushToken: string }> = [];

      for (const user of crmUsersWithTokens) {
        if (!seenTokens.has(user.pushToken)) {
          seenTokens.add(user.pushToken);
          usersWithTokens.push(user);
        }
      }
      for (const manager of mobileManagersWithTokens) {
        if (!seenTokens.has(manager.pushToken!)) {
          seenTokens.add(manager.pushToken!);
          usersWithTokens.push({ id: manager.id, pushToken: manager.pushToken! });
        }
      }

      if (usersWithTokens.length === 0) {
        this.logger.warn(`No merchant staff with push tokens for merchant ${merchantId}`);
        return;
      }

      // Filter out users who explicitly disabled push for this printer (opt-out model)
      const disabledPrefs = await this.prisma.staffPrinterNotification.findMany({
        where: {
          merchantId,
          printerId: printerInfo.id,
          pushEnabled: false,
        },
        select: { userId: true },
      });

      if (disabledPrefs.length > 0) {
        const disabledUserIds = new Set(disabledPrefs.map((p) => p.userId));
        const before = usersWithTokens.length;
        // Mutate in place — filter out disabled users
        for (let i = usersWithTokens.length - 1; i >= 0; i--) {
          if (disabledUserIds.has(usersWithTokens[i].id)) {
            usersWithTokens.splice(i, 1);
          }
        }
        if (usersWithTokens.length < before) {
          this.logger.log(
            `Filtered out ${before - usersWithTokens.length} staff members who disabled notifications for printer ${printerInfo.id}`,
          );
        }
      }

      if (usersWithTokens.length === 0) {
        this.logger.warn(`All staff disabled notifications for printer ${printerInfo.id} at merchant ${merchantId}`);
        return;
      }

      const template: NotificationTemplate = {
        title: '🔔 Order Ready!',
        body: `Order #${orderId.slice(0, 8)} at ${printerInfo.name || 'Table'} is ready for pickup`,
        data: {
          type: 'merchant_order_ready',
          orderId,
          merchantId,
          printerId: printerInfo.id.toString(),
          status,
        },
        channelId: 'order',
        priority: 'high',
      };

      // Create Expo push messages
      const messages: ExpoPushMessage[] = usersWithTokens.map((user: any) => ({
        to: user.pushToken!,
        title: template.title,
        body: template.body,
        data: template.data || {},
        sound: 'default',
        priority: 'high',
        channelId: 'order',
      }));

      // Send notifications in chunks
      const chunks = this.expo.chunkPushNotifications(messages);
      const tickets: ExpoPushTicket[] = [];

      for (const chunk of chunks) {
        try {
          const ticketChunk = await this.expo.sendPushNotificationsAsync(chunk);
          tickets.push(...ticketChunk);
        } catch (error) {
          this.logger.error('Error sending merchant staff notification chunk:', error);
        }
      }

      // Process tickets for errors
      await this.processTickets(tickets, usersWithTokens);

      this.logger.log(`Sent order ready notifications to ${usersWithTokens.length} staff members for order ${orderId}`);
    } catch (error) {
      this.logger.error('Error sending merchant staff notification:', error);
    }
  }

  /**
   * Send notification to merchant staff when customer sends a chat message
   */
  async sendMerchantChatNotification(
    merchantId: string,
    chatId: string,
    messageContent: string,
    senderName: string,
  ): Promise<void> {
    try {
      // Get merchant staff with roles that should receive chat notifications
      const staffMembers = await this.prisma.merchantMember.findMany({
        where: {
          merchantId,
          isActive: true,
          role: {
            in: ['OWNER', 'ADMIN', 'MANAGER', 'SUPPORT'],
          },
        },
        include: {
          crmUser: {
            include: {
              user: true,
            },
          },
        },
      });

      const usersWithTokens = staffMembers
        .filter((member: any) => member.crmUser?.user?.pushToken)
        .map((member: any) => member.crmUser.user);

      if (usersWithTokens.length === 0) {
        this.logger.warn(`No merchant staff with push tokens for chat notification, merchant ${merchantId}`);
        return;
      }

      const template: NotificationTemplate = {
        title: `💬 New message from ${senderName}`,
        body: messageContent.length > 100 ? messageContent.slice(0, 100) + '...' : messageContent,
        data: {
          type: 'merchant_chat_message',
          chatId,
          merchantId,
          senderName,
        },
        channelId: 'chat',
        priority: 'high',
      };

      const messages: ExpoPushMessage[] = usersWithTokens.map((user: any) => ({
        to: user.pushToken!,
        title: template.title,
        body: template.body,
        data: template.data || {},
        sound: 'default',
        priority: 'high',
        channelId: 'chat',
      }));

      const chunks = this.expo.chunkPushNotifications(messages);

      for (const chunk of chunks) {
        try {
          await this.expo.sendPushNotificationsAsync(chunk);
        } catch (error) {
          this.logger.error('Error sending merchant chat notification chunk:', error);
        }
      }

      this.logger.log(`Sent chat notifications to ${usersWithTokens.length} staff members for chat ${chatId}`);
    } catch (error) {
      this.logger.error('Error sending merchant chat notification:', error);
    }
  }

  // Utility method to get all users with push tokens for a merchant
  async getMerchantUsers(merchantId: string): Promise<string[]> {
    const users = await this.prisma.user.findMany({
      where: {
        pushToken: { not: null },
        muteNotifications: false,
        merchantSettings: {
          some: {
            merchantId,
            allowPromo: true
          }
        }
      },
      select: { id: true }
    });

    return users.map((u: any) => u.id);
  }
}