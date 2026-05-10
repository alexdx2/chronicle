import { SentryModule } from '@sentry/nestjs/setup';
import { GraphqlModule } from './graphql/graphql.module';
import { MiddlewareConsumer, Module, NestModule } from '@nestjs/common';
import { PrinterController } from './controllers/printer.controller';
import { AuthMiddleware } from './middleware/auth.middleware';
import { APP_FILTER, APP_INTERCEPTOR } from '@nestjs/core';
import { ErrorHandler } from './middleware/errorHandler';
import { RefactoredPrinterFactory } from './services/refactored-printer.service';
import { PrinterCommunicationService } from './services/printer-communication.service';
import { AuthController } from './controllers/auth.controller';
import { MerchantController } from './controllers/merchant.controller';
import { UserController } from './controllers/user.controller';
import { VoucherController } from './controllers/voucher.controller';
import { AuthService } from './services/auth.service';
import { VoucherService } from './services/voucher.service';
import { SocketFactory } from './socket-handlers/factory.socket';
import { WebSocketGateway } from './gateways/websocket.gateway';
import { MobileWebSocketGateway } from './gateways/mobile.gateway';
import { CrmWebSocketGateway } from './gateways/crm.gateway';
import { ConfigModule, ConfigService } from '@nestjs/config';
import { redisProvider } from './providers/redis.provider';
import { PrismaService } from './prisma.service';
import { BalanceService } from './services/balance.service';
import { SocketModule } from './socket.module';
import { LoggingMiddleware } from './middleware/logging.middleware';
import { AuthModule } from './auth/auth.module';
import { PrinterRedisService } from './services/reddis/printer.reddis';
import { VoucherProcessor } from './bull/voucher.bull';
import { TwilioModule } from 'nestjs-twilio';
import { SmsService } from './services/helper/sms.service';
import { PhoneController } from './controllers/phone.controller';
import { CloudinaryService } from './services/file/cloudinary.service';
import { UploadController } from './controllers/file.controller';
import { ImageKitService } from './services/file/imagekit.service';
import { ServeStaticModule } from '@nestjs/serve-static';
import { HealthController } from './controllers/health.controller';
import { WheelController } from './controllers/wheel.controller';
import { SurpriseBoxController } from './controllers/surprisebox.controller';
import { OrderService } from './services/order.service';
import { PromotionService } from './services/promotion.service';
import { OrderRedisService } from './services/reddis/order.reddis';
import { OrderPubSubService } from './services/pubsub/order.pubsub';
import { VoucherQueueService } from './services/voucher-queue.service';
import { OtpService } from './services/otp.service';
import { GroupManagementService } from './services/group-management.service';
import { GroupCleanupService } from './services/group-cleanup.service';
import { UserSessionService } from './services/user-session.service';
import { VoucherApplicationService } from './services/voucher-application.service';
import { VoucherExpiryService } from './services/voucher-expiry.service';
import { NotificationService } from './services/notification.service';
import { NotificationController } from './controllers/notification.controller';
import { PointsRewardService } from './services/points-reward.service';
import { AnonymousUserService } from './services/anonymous-user.service';
import { CentrifugoService } from './services/centrifugo.service';
import { CentrifugoController } from './controllers/centrifugo.controller';
import { ChatService } from './services/chat.service';
import { PushService } from './services/push.service';
import { UserNotificationService } from './services/user-notification.service';
import { NotificationFacadeService } from './services/notification-facade.service';
// import { LocationService } from './services/location.service';
import { ScheduleModule } from '@nestjs/schedule';
import { RequestContextInterceptor } from './shared/request-context.interceptor';
import { DatabaseInitService } from './services/database-init.service';
import { StripeService } from './services/stripe.service';
import { PaymentService } from './services/payment.service';
import { StripeConnectService } from './services/stripe-connect.service';
import { PaymentResolver } from './graphql/resolvers/payment.resolver';
import { StripeWebhookController } from './controllers/stripe-webhook.controller';
import { CampaignService } from './services/campaign.service';
import { CampaignResolver } from './graphql/resolvers/campaign.resolver';
@Module({
    imports: [
        SentryModule.forRoot(),
        // ServeStaticModule.forRoot({
        //     rootPath: join(__dirname, '..', 'tmp', 'uploads'),
        //     serveRoot: '/uploads/temp',
        // }),
        ConfigModule.forRoot({ isGlobal: true }),
        ScheduleModule.forRoot(),
        SocketModule,
        AuthModule,
        GraphqlModule,
        TwilioModule.forRootAsync({
            imports: [ConfigModule],
            useFactory: (cfg: ConfigService) => ({
                accountSid: cfg.get('TWILIO_ACCOUNT_SID'),
                authToken: cfg.get('TWILIO_AUTH_TOKEN'),
            }),
            inject: [ConfigService],
        }),
        // BullModule.forRootAsync({
        //     imports: [ConfigModule],
        //     useFactory: (config: ConfigService) => ({
        //         connection: {
        //             host: config.get('REDIS_HOST') ?? 'localhost',
        //             port: config.get('REDIS_PORT') ?? 6379,
        //             password: config.get('REDIS_PASSWORD'),
        //         },
        //     }),
        //     inject: [ConfigService],
        // }),
    ],
    controllers: [
        PrinterController,
        AuthController,
        MerchantController,
        UserController,
        VoucherController,
        PhoneController,
        UploadController,
        HealthController,
        WheelController,
        SurpriseBoxController,
        NotificationController,
        CentrifugoController,
        StripeWebhookController,
    ],
    providers: [
        redisProvider,
        // SocketService moved to SocketModule (global)
        PrinterRedisService,
        BalanceService,
        PrismaService,
        DatabaseInitService,
        VoucherService,
        AuthService,
        RefactoredPrinterFactory,
        PrinterCommunicationService,
        SmsService,
        CloudinaryService,
        ImageKitService,
        OrderService,
        PromotionService,
        OrderRedisService,
        OrderPubSubService,
        UserSessionService,
        VoucherApplicationService,
        VoucherExpiryService,
        NotificationService,
        VoucherQueueService,
        GroupManagementService,
        GroupCleanupService,
        OtpService,
        PointsRewardService,
        AnonymousUserService,
        CentrifugoService,
        ChatService,
        PushService,
        UserNotificationService,
        NotificationFacadeService,
        StripeService,
        PaymentService,
        StripeConnectService,
        PaymentResolver,
        // LocationService,
        SocketFactory,
        WebSocketGateway,
        MobileWebSocketGateway,
        CrmWebSocketGateway,
        {
            provide: APP_FILTER,
            useClass: ErrorHandler,
        },
        {
            provide: APP_INTERCEPTOR,
            useClass: RequestContextInterceptor,
        },
        VoucherProcessor,
        CampaignService,
        CampaignResolver,
    ],
})
export class AppModule implements NestModule {
    configure(consumer: MiddlewareConsumer) {
        consumer
            .apply(LoggingMiddleware)
            .forRoutes('/');
        consumer.apply(AuthMiddleware).forRoutes(PrinterController);
        consumer.apply(AuthMiddleware).forRoutes(UserController);
        consumer.apply(AuthMiddleware).forRoutes(VoucherController);
        consumer.apply(AuthMiddleware).forRoutes(MerchantController);
    }
}