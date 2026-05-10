import { BadRequestException, Injectable, NotFoundException } from "@nestjs/common";
import { DeliveryMode, Order, OrderItem, OrderItemState, OrderState, PaymentMethod, PaymentStatus, PrismaClient, UserVoucher, UserVoucherStatus, VoucherTemplate } from "@prisma/client";
import { PrinterRedisService, UserContext } from "./reddis/printer.reddis";
import { UserPayload } from "../models/user.model";
import Logger from "../logger";
import { logger } from "../logger";
import { PrismaService } from "../prisma.service";
import { OrderRedisService } from "./reddis/order.reddis";
import { SocketService, OrderUpdateEvent, OrderCreatedEvent, PointsClaimedRewardPayload } from "./socket.service";
import { VoucherApplicationService } from "./voucher-application.service";
import { NotificationService } from "./notification.service";
import { PointsRewardService } from "./points-reward.service";
import { OrderModel, CartItem, CreateOrderParams } from "../models/order.model";
import { OrderStateMachine } from "../models/value-objects/order-state-machine";
import { VoucherQueueService } from "./voucher-queue.service";
import { SOCKET_EVENTS } from "../shared/socket-events";
import { ChatService } from "./chat.service";
import { PromotionService } from "./promotion.service";
import { evaluatePromotions, AppliedPromoDetail } from "@otopoint/pricing-engine";

export interface CreateOrder {
    paymentMethod: PaymentMethod;
    deliveryMethod: DeliveryMode;
    userVoucherIds?: string[];  // Multiple vouchers to apply
}

// Re-export CartItem from OrderModel for backward compatibility
export { CartItem } from "../models/order.model";
@Injectable()
export class OrderService {
    constructor(
        private readonly prisma: PrismaService,
        private readonly printerRedisService: PrinterRedisService,
        private readonly orderRedisService: OrderRedisService,
        private readonly socketService: SocketService,
        private readonly voucherApplicationService: VoucherApplicationService,
        private readonly notificationService: NotificationService,
        private readonly pointsRewardService: PointsRewardService,
        private readonly voucherQueueService: VoucherQueueService,
        private readonly chatService: ChatService,
        private readonly promotionService: PromotionService
    ) {

    }

    /**
     * Evaluate promotions for cart items and apply discounts.
     * Returns modified items and applied promo details for persistence.
     */
    private async evaluateAndApplyPromotions(
        merchantId: string,
        items: CartItem[],
        correlationId: string,
    ): Promise<{ items: CartItem[]; appliedPromos: AppliedPromoDetail[] }> {
        const activePromos = await this.promotionService.getActivePromotions(merchantId);

        if (activePromos.length === 0) {
            logger.order('debug', '🏷️ No active promotions for merchant', { correlationId, merchantId });
            return { items, appliedPromos: [] };
        }

        logger.order('debug', `🏷️ Evaluating ${activePromos.length} promotions`, { correlationId });

        // Convert CartItems to OrderCalculationItems for the engine
        const calcItems = items.map(item => ({
            id: item.cartItemId,
            price: item.price,
            name: item.name,
            menuItemId: item.menuItemId,
            cartItemId: item.cartItemId,
            category: item.category,
            bundleInstanceId: item.bundleInstanceId,
            bundleSlotIndex: item.bundleSlotIndex,
        }));

        const result = evaluatePromotions(calcItems, activePromos);

        if (result.appliedPromos.length > 0) {
            logger.order('info', `🏷️ ✅ Applied ${result.appliedPromos.length} promotions, discount: ${result.totalPromoDiscount}`, {
                correlationId,
                appliedPromos: result.appliedPromos.map(p => ({ id: p.promotionId, name: p.promotionName, discount: p.discountAmount })),
            });
        }

        // Map promo-adjusted prices back onto CartItems
        const promoItemMap = new Map(result.items.map(i => [i.id, i]));
        const updatedItems = items.map(item => {
            const promoItem = promoItemMap.get(item.cartItemId);
            if (promoItem && promoItem.promoDiscount && promoItem.promoDiscount > 0) {
                return {
                    ...item,
                    price: promoItem.price,
                    promoDiscount: promoItem.promoDiscount,
                    prePromoPrice: promoItem.prePromoPrice,
                };
            }
            return item;
        });

        return { items: updatedItems, appliedPromos: result.appliedPromos };
    }

    async createOrderFromGroup(user: UserPayload, merchantId: string, groupId: string, args: CreateOrder): Promise<Order & { items: OrderItem[] }> {
        const correlationId = logger.generateCorrelationId();
        const startTime = Date.now();

        logger.order('info', 'Creating group order started', {
            correlationId,
            userId: user.id,
            merchantId,
            groupId,
            paymentMethod: args.paymentMethod,
            deliveryMethod: args.deliveryMethod
        });

        try {
            // 1. Validate group and ownership
            const group = await this.printerRedisService.getGroup(groupId);
            if (!group) {
                logger.order('warn', 'Group order failed - group not found', {
                    correlationId,
                    userId: user.id,
                    groupId
                });
                throw new Error("Group not found");
            }

            if (group.ownerId !== user.id) {
                logger.order('warn', 'Group order failed - user not group owner', {
                    correlationId,
                    userId: user.id,
                    groupId,
                    groupOwnerId: group.ownerId
                });
                throw new Error("You are not the owner of the group");
            }

            logger.order('debug', 'Group validation passed, processing contexts', {
                correlationId,
                userId: user.id,
                groupId,
                groupMemberCount: group.members?.length || 0
            });

            // 2. Get all group member contexts
            const contexts: Record<string, UserContext> = await this.printerRedisService.getGroupContexts(groupId);

            if (!contexts || Object.keys(contexts).length === 0) {
                logger.order('warn', 'Group order failed - no contexts found', {
                    correlationId,
                    userId: user.id,
                    groupId
                });
                throw new Error("No order contexts found for group");
            }

            // 3. Load all menu items needed for the group order
            const allMenuItemIds = new Set<string>();
            for (const context of Object.values(contexts)) {
                context.items.forEach(item => allMenuItemIds.add(item.menuItemId));
            }

            const menuItems = await this.prisma.menuItem.findMany({
                where: {
                    id: {
                        in: Array.from(allMenuItemIds),
                    },
                },
            });

            // 4. Collect activated vouchers from all group members
            const allActivatedVoucherIds: string[] = [];

            logger.order('info', '🎫 VOUCHER COLLECTION START FOR GROUP ORDER', {
                correlationId,
                groupId,
                groupMemberCount: Object.keys(contexts).length
            });

            // Get FREE_ITEM vouchers from each member's Redis queue
            // OPTIMIZATION: Collect all voucher IDs first, then batch fetch
            const allMemberVoucherIds: string[] = [];
            for (const memberId of Object.keys(contexts)) {
                const memberVouchers = await this.voucherQueueService.getVouchersByUser(memberId);

                logger.order('debug', '🎫 Checking member activated vouchers', {
                    correlationId,
                    memberId,
                    activatedVoucherCount: memberVouchers.length
                });

                allMemberVoucherIds.push(...memberVouchers.map(q => q.voucherId));
            }

            // OPTIMIZATION: Batch fetch all vouchers at once instead of N individual queries
            let freeItemVouchers: Array<UserVoucher & { template: VoucherTemplate }> = [];
            if (allMemberVoucherIds.length > 0) {
                freeItemVouchers = await this.prisma.userVoucher.findMany({
                    where: {
                        id: { in: allMemberVoucherIds },
                        template: { type: 'FREE_ITEM' } // Filter FREE_ITEM at DB level
                    },
                    include: { template: true }
                });

                logger.order('debug', '🎫 Batch fetched member vouchers', {
                    correlationId,
                    totalVoucherIds: allMemberVoucherIds.length,
                    freeItemVouchersFound: freeItemVouchers.length
                });
            }

            const freeItemVoucherIds = freeItemVouchers.map(v => v.id);
            allActivatedVoucherIds.push(...freeItemVoucherIds);

            logger.order('debug', '🎫 Collected member FREE_ITEM vouchers', {
                correlationId,
                totalMembers: Object.keys(contexts).length,
                freeItemVoucherCount: freeItemVoucherIds.length,
                voucherIds: freeItemVoucherIds
            });

            // Add group owner's DISCOUNT vouchers from args
            if (args.userVoucherIds) {
                allActivatedVoucherIds.push(...args.userVoucherIds);

                logger.order('debug', '🎫 Added group owner DISCOUNT vouchers', {
                    correlationId,
                    groupOwnerId: user.id,
                    discountVoucherCount: args.userVoucherIds.length,
                    voucherIds: args.userVoucherIds
                });
            }

            logger.order('info', '🎫 VOUCHER COLLECTION COMPLETE', {
                correlationId,
                groupId,
                totalVouchers: allActivatedVoucherIds.length,
                groupMemberCount: Object.keys(contexts).length
            });

            // 5. Create order domain model from group contexts
            const orderModel = OrderModel.fromGroupContexts(
                contexts,
                menuItems,
                {
                    paymentMethod: args.paymentMethod,
                    deliveryMethod: args.deliveryMethod,
                    userVoucherIds: args.userVoucherIds
                },
                {
                    merchantId,
                    userId: user.id,
                    groupId
                }
            );

            // 5.5. Evaluate promotions (before vouchers — promos modify item prices)
            const promoResult = await this.evaluateAndApplyPromotions(
                merchantId,
                orderModel.getItems(),
                correlationId,
            );
            if (promoResult.appliedPromos.length > 0) {
                orderModel.replaceItems(promoResult.items);
            }

            // 6. Validate and apply all collected vouchers
            // OPTIMIZATION: Batch fetch all vouchers at once, then validate with entities
            let appliedVouchers: Array<UserVoucher & { template: VoucherTemplate }> = [];

            if (allActivatedVoucherIds.length > 0) {
                // Batch fetch all vouchers (both FREE_ITEM and DISCOUNT) in one query
                const allVouchers = await this.prisma.userVoucher.findMany({
                    where: { id: { in: allActivatedVoucherIds } },
                    include: { template: true }
                });

                logger.order('info', '🎫 Batch fetched all vouchers for validation', {
                    correlationId,
                    groupId,
                    requestedVouchers: allActivatedVoucherIds.length,
                    foundVouchers: allVouchers.length
                });

                // Create voucher map for quick lookup
                const voucherMap = new Map(allVouchers.map(v => [v.id, v]));

                // Validate each voucher (no DB calls during validation)
                const vouchers = await Promise.all(
                    allActivatedVoucherIds.map(async (voucherId, index) => {
                        const voucher = voucherMap.get(voucherId);

                        if (!voucher) {
                            throw new Error(`Voucher not found: ${voucherId}`);
                        }

                        logger.order('debug', `🎫 Validating voucher ${index + 1}/${allActivatedVoucherIds.length}`, {
                            correlationId,
                            voucherId,
                            voucherName: voucher.template.name,
                            voucherType: voucher.template.type,
                            voucherOwner: voucher.userId
                        });

                        // OPTIMIZATION: Validate using entity (no DB fetch inside)
                        const validationResult = this.voucherApplicationService.validateVoucherWithEntity(
                            voucher, // Pass entity directly
                            voucher.userId, // Use owner from entity
                            merchantId,
                            orderModel.getItems()
                        );

                        if (!validationResult.isValid) {
                            logger.order('error', '🎫 ❌ VOUCHER VALIDATION FAILED', {
                                correlationId,
                                voucherId,
                                error: validationResult.error
                            });
                            throw new Error(`Voucher ${voucherId}: ${validationResult.error}`);
                        }

                        logger.order('info', '🎫 ✅ VOUCHER VALIDATION SUCCESS', {
                            correlationId,
                            voucherId,
                            voucherName: voucher.template.name,
                            voucherType: voucher.template.type
                        });

                        return voucher; // Return the entity we already have
                    })
                );

                appliedVouchers = vouchers;

                // Apply all vouchers using domain model
                const voucherResult = orderModel.applyVouchers(vouchers);

                logger.order('info', '🎫 ✅ GROUP VOUCHERS APPLIED', {
                    correlationId,
                    groupId,
                    totalVouchers: allActivatedVoucherIds.length,
                    freeItemVouchers: vouchers.filter(v => v.template.type === 'FREE_ITEM').length,
                    discountVouchers: vouchers.filter(v => v.template.type === 'DISCOUNT').length,
                    voucherDiscount: voucherResult.pricing.voucherDiscount,
                    freeItemsAdded: voucherResult.processedItems.filter(item => item.isVoucherItem).length
                });
            }

            // 7. Get pricing from domain model
            const pricing = orderModel.getPricing();

            logger.order('info', 'Creating group order in database', {
                correlationId,
                userId: user.id,
                merchantId,
                groupId,
                itemCount: orderModel.getItems().length,
                subtotal: pricing.subtotal,
                voucherDiscount: pricing.voucherDiscount,
                total: pricing.total,
                paymentMethod: args.paymentMethod,
                deliveryMethod: args.deliveryMethod
            });

            // 8. Create order in transaction with voucher updates
            const order = await this.prisma.$transaction(async (tx) => {
                const newOrder = await tx.order.create({
                    data: orderModel.toPrismaCreateInput(),
                    include: {
                        items: {
                            include: {
                                appliedVoucher: { include: { template: true }}
                            }
                        },
                        appliedVoucher: { include: { template: true }}
                    },
                });

                // Update voucher statuses to PENDING
                // Only update vouchers that were actually applied (produced a discount or free item)
                const actuallyAppliedIds = new Set(orderModel.getAppliedVoucherIds());
                const vouchersToUpdate = appliedVouchers.filter(v => actuallyAppliedIds.has(v.id));

                if (vouchersToUpdate.length > 0) {
                    logger.order('info', '🎫 🔄 UPDATING VOUCHER STATUS TO PENDING', {
                        correlationId,
                        groupId,
                        orderId: newOrder.id,
                        requestedVouchers: appliedVouchers.length,
                        actuallyApplied: vouchersToUpdate.length,
                        voucherCount: vouchersToUpdate.length
                    });

                    const voucherUpdateResults = await Promise.allSettled(
                        vouchersToUpdate.map(async (voucher) => {
                            try {
                                await tx.userVoucher.update({
                                    where: { id: voucher.id },
                                    data: {
                                        status: UserVoucherStatus.PENDING,
                                        orderId: newOrder.id,
                                        usedAt: new Date()
                                    }
                                });

                                // Remove from Redis queue
                                const queueItems = await this.voucherQueueService.getVouchersByUser(voucher.userId);
                                const queueItem = queueItems.find(q => q.voucherId === voucher.id);
                                if (queueItem) {
                                    await this.printerRedisService.removeVoucher(queueItem.voucherUid);
                                }

                                return { success: true, voucherId: voucher.id };
                            } catch (error) {
                                logger.order('error', '🎫 Failed to update voucher', {
                                    correlationId,
                                    voucherId: voucher.id,
                                    error: error instanceof Error ? error.message : String(error)
                                });
                                throw new Error(`Failed to update voucher ${voucher.id}: ${error instanceof Error ? error.message : String(error)}`);
                            }
                        })
                    );

                    const failedUpdates = voucherUpdateResults.filter(r => r.status === 'rejected');
                    if (failedUpdates.length > 0) {
                        const errorMessages = failedUpdates.map(f =>
                            f.status === 'rejected' ? f.reason : 'Unknown error'
                        ).join(', ');

                        logger.order('error', '🎫 ❌ TRANSACTION ROLLBACK: Some voucher updates failed', {
                            correlationId,
                            orderId: newOrder.id,
                            failedCount: failedUpdates.length
                        });

                        throw new Error(`Failed to update vouchers: ${errorMessages}`);
                    }

                    logger.order('info', '🎫 ✅ ALL VOUCHERS UPDATED', {
                        correlationId,
                        orderId: newOrder.id,
                        voucherCount: vouchersToUpdate.length
                    });
                }

                // Persist promotion audit trail
                if (promoResult.appliedPromos.length > 0) {
                    const orderItemMap = new Map(
                        newOrder.items.map(oi => [oi.cartItemId, oi.id])
                    );
                    // Use actual per-item discount from engine (not even split)
                    const itemDiscountMap = new Map(
                        promoResult.items.map(i => [i.cartItemId, i.promoDiscount ?? 0])
                    );
                    const promoAppliedData = promoResult.appliedPromos.flatMap(promo =>
                        promo.affectedItemIds
                            .filter(cartItemId => orderItemMap.has(cartItemId))
                            .map(cartItemId => ({
                                promotionId: promo.promotionId,
                                orderItemId: orderItemMap.get(cartItemId)!,
                                discountAmount: itemDiscountMap.get(cartItemId) ?? 0,
                            }))
                    );
                    if (promoAppliedData.length > 0) {
                        await tx.promotionAppliedItem.createMany({ data: promoAppliedData });
                    }
                }

                return newOrder;
            }, {
                maxWait: 5000,
                timeout: 10000,
            });

            // 9. Publish order created event (unified Redis + Socket.io)
            await this.publishOrderCreatedEvent(order as any);

            // 10. Emit voucher updates for all members who used vouchers
            if (appliedVouchers.length > 0) {
                const affectedUserIds = new Set<string>();

                appliedVouchers.forEach(v => affectedUserIds.add(v.userId));

                for (const userId of affectedUserIds) {
                    await this.emitVoucherUpdate(userId, merchantId, group.printerId?.toString());
                }

                logger.order('info', '🎫 Voucher updates emitted', {
                    correlationId,
                    groupId,
                    affectedUsers: affectedUserIds.size
                });
            }

            // 11. Emit order-purchased event to all group members
            const groupMemberIds = group.members || [];
            for (const memberId of groupMemberIds) {
                await this.socketService.emitToUser(memberId, SOCKET_EVENTS.ORDER.PURCHASED, {
                    orderId: order.id,
                    merchantId,
                    printerId: group.printerId,
                    total: order.total,
                    itemCount: order.items.length,
                    state: order.state,
                    paymentStatus: order.paymentStatus,
                    createdAt: order.createdAt,
                    isGroupOrder: true,
                    groupId
                });
            }

            // 12. Create order placeholder in chat
            try {
                await this.chatService.createOrderPlaceholder(user.id, merchantId, order.id);
                logger.order('info', 'Order placeholder created in chat', { correlationId, orderId: order.id });
            } catch (error) {
                logger.order('warn', 'Failed to create order placeholder in chat', {
                    correlationId,
                    orderId: order.id,
                    error: error instanceof Error ? error.message : String(error)
                });
                // Don't fail order creation if chat placeholder fails
            }

            logger.order('info', 'Group order created successfully', {
                correlationId,
                userId: user.id,
                merchantId,
                groupId,
                orderId: order.id,
                itemCount: order.items.length,
                orderTotal: order.total,
                vouchersApplied: appliedVouchers.length,
                duration: Date.now() - startTime
            });

            return order as Order & { items: OrderItem[] };
        } catch (error: any) {
            logger.order('error', 'Group order creation failed', {
                correlationId,
                userId: user.id,
                merchantId,
                groupId,
                error: error.message,
                duration: Date.now() - startTime
            });
            throw error;
        }
    }

    async createOrderFromCart(user: UserPayload, merchantId: string, printerId: string | undefined, cart: CartItem[], args: CreateOrder): Promise<Order & { items: OrderItem[] }> {
        const correlationId = logger.generateCorrelationId();
        const startTime = Date.now();

        logger.order('info', 'Creating cart order started', {
            correlationId,
            userId: user.id,
            merchantId,
            printerId,
            cartItemCount: cart.length,
            paymentMethod: args.paymentMethod,
            deliveryMethod: args.deliveryMethod,
            userVoucherIds: args.userVoucherIds
        });

        try {
            // 1. Validate merchant context and printer mode if printerId provided
            if (printerId) {
                const userContext = await this.printerRedisService.getUserContext(printerId, user.id);
                if (!userContext) {
                    logger.order('info', 'User ordering without active lobby context', {
                        correlationId,
                        userId: user.id,
                        merchantId,
                        printerId
                    });
                }

            }

            // 2. Load menu items for validation
            const menuItems = await this.prisma.menuItem.findMany({
                where: {
                    id: {
                        in: cart.map((item) => item.menuItemId),
                    },
                },
            });

            // 3. Create order domain model (handles validation + pricing)
            const orderModel = OrderModel.fromCart(
                cart,
                menuItems,
                {
                    paymentMethod: args.paymentMethod,
                    deliveryMethod: args.deliveryMethod,
                    userVoucherIds: args.userVoucherIds
                },
                {
                    merchantId,
                    userId: user.id,
                    printerId: printerId ? parseInt(printerId) : undefined
                }
            );

            // 3.5. Evaluate promotions (before vouchers — promos modify item prices)
            const promoResult = await this.evaluateAndApplyPromotions(
                merchantId,
                orderModel.getItems(),
                correlationId,
            );
            if (promoResult.appliedPromos.length > 0) {
                orderModel.replaceItems(promoResult.items);
            }

            // 4. Load and apply vouchers if provided
            // OPTIMIZATION: Batch fetch vouchers, then validate with entities
            let appliedVouchers: Array<UserVoucher & { template: VoucherTemplate }> = [];
            const voucherIdsToUse = args.userVoucherIds || [];

            logger.order('info', '🎫 VOUCHER PROCESSING START', {
                correlationId,
                userId: user.id,
                userVoucherIds: voucherIdsToUse,
                voucherCount: voucherIdsToUse.length,
                hasVouchers: voucherIdsToUse.length > 0
            });

            if (voucherIdsToUse.length > 0) {
                // OPTIMIZATION: Batch fetch all vouchers at once instead of N individual queries
                const allVouchers = await this.prisma.userVoucher.findMany({
                    where: { id: { in: voucherIdsToUse } },
                    include: { template: true }
                });

                logger.order('info', '🎫 Batch fetched all vouchers for cart order', {
                    correlationId,
                    userId: user.id,
                    requestedVouchers: voucherIdsToUse.length,
                    foundVouchers: allVouchers.length
                });

                // Create voucher map for quick lookup
                const voucherMap = new Map(allVouchers.map(v => [v.id, v]));

                // Validate each voucher using pre-fetched entities (no DB calls during validation)
                const vouchers = await Promise.all(
                    voucherIdsToUse.map(async (voucherId, index) => {
                        const voucher = voucherMap.get(voucherId);

                        if (!voucher) {
                            throw new Error(`Voucher not found: ${voucherId}`);
                        }

                        logger.order('debug', `🎫 Validating voucher ${index + 1}/${voucherIdsToUse.length}`, {
                            correlationId,
                            voucherId,
                            voucherName: voucher.template.name,
                            voucherType: voucher.template.type,
                            userId: user.id,
                            merchantId
                        });

                        // OPTIMIZATION: Validate using entity (no DB fetch inside)
                        const validationResult = this.voucherApplicationService.validateVoucherWithEntity(
                            voucher, // Pass entity directly
                            user.id,
                            merchantId,
                            orderModel.getItems()
                        );

                        if (!validationResult.isValid) {
                            logger.order('error', '🎫 ❌ VOUCHER VALIDATION FAILED', {
                                correlationId,
                                userId: user.id,
                                voucherId,
                                error: validationResult.error
                            });
                            throw new Error(`Voucher ${voucherId}: ${validationResult.error}`);
                        }

                        logger.order('info', '🎫 ✅ VOUCHER VALIDATION SUCCESS', {
                            correlationId,
                            voucherId,
                            voucherName: voucher.template.name,
                            voucherType: voucher.template.type
                        });

                        return voucher; // Return the entity we already have
                    })
                );

                appliedVouchers = vouchers;

                // Apply vouchers using domain model
                const voucherResult = orderModel.applyVouchers(vouchers);

                logger.order('info', '🎫 ✅ VOUCHERS APPLIED SUCCESSFULLY', {
                    correlationId,
                    userId: user.id,
                    requestedVoucherIds: voucherIdsToUse,
                    appliedVoucherIds: voucherResult.appliedVoucherIds,
                    voucherDiscount: voucherResult.pricing.voucherDiscount,
                    voucherBreakdown: voucherResult.voucherBreakdown,
                    freeItemsAdded: voucherResult.processedItems.filter(item => item.isVoucherItem).length
                });
            }

            // 5. Get pricing from domain model
            const pricing = orderModel.getPricing();

            logger.order('info', 'Creating cart order in database', {
                correlationId,
                userId: user.id,
                merchantId,
                printerId,
                itemCount: orderModel.getItems().length,
                subtotal: pricing.subtotal,
                voucherDiscount: pricing.voucherDiscount,
                total: pricing.total
            });

            // 6. Create order in transaction
            const order = await this.prisma.$transaction(async (tx) => {
                // Create the order using domain model
                const newOrder = await tx.order.create({
                    data: orderModel.toPrismaCreateInput(),
                    include: {
                        items: {
                            include: {
                                appliedVoucher: {
                                    include: {
                                        template: true
                                    }
                                }
                            }
                        },
                        appliedVoucher: {
                            include: {
                                template: true
                            }
                        }
                    },
                });

                // Update voucher statuses within the same transaction
                // Only update vouchers that were actually applied (produced a discount or free item)
                const actuallyAppliedIds = new Set(orderModel.getAppliedVoucherIds());
                const vouchersToUpdate = appliedVouchers.filter(v => actuallyAppliedIds.has(v.id));

                if (vouchersToUpdate.length > 0) {
                    logger.order('info', '🎫 🔄 UPDATING VOUCHER STATUS TO PENDING (IN TRANSACTION)', {
                        correlationId,
                        userId: user.id,
                        orderId: newOrder.id,
                        requestedVouchers: appliedVouchers.length,
                        actuallyApplied: vouchersToUpdate.length,
                        skippedVouchers: appliedVouchers.length - vouchersToUpdate.length,
                        vouchersToUpdate: vouchersToUpdate.map(v => ({
                            id: v.id,
                            name: v.template?.name
                        }))
                    });

                    const voucherUpdateResults = await Promise.allSettled(
                        vouchersToUpdate.map(async (voucher) => {
                            try {
                                const result = await tx.userVoucher.updateMany({
                                    where: {
                                        id: voucher.id,
                                        status: UserVoucherStatus.AVAILABLE,
                                    },
                                    data: {
                                        status: UserVoucherStatus.PENDING,
                                        orderId: newOrder.id,
                                        usedAt: new Date()
                                    }
                                });
                                if (result.count === 0) {
                                    logger.order('warn', '🎫 Voucher claim conflict - already claimed by concurrent request', {
                                        correlationId,
                                        voucherId: voucher.id,
                                        orderId: newOrder.id,
                                    });
                                    throw new BadRequestException('Voucher no longer available');
                                }
                                return { success: true, voucherId: voucher.id };
                            } catch (error) {
                                logger.order('error', '🎫 Failed to update voucher in transaction', {
                                    correlationId,
                                    voucherId: voucher.id,
                                    orderId: newOrder.id,
                                    error: error instanceof Error ? error.message : String(error)
                                });
                                throw error;
                            }
                        })
                    );

                    const failedUpdates = voucherUpdateResults.filter(result => result.status === 'rejected');
                    if (failedUpdates.length > 0) {
                        const errorMessages = failedUpdates.map(f =>
                            f.status === 'rejected' ? f.reason : 'Unknown error'
                        ).join(', ');

                        logger.order('error', '🎫 ❌ TRANSACTION ROLLBACK: Some voucher updates failed', {
                            correlationId,
                            orderId: newOrder.id,
                            failedCount: failedUpdates.length
                        });

                        throw new Error(`Failed to update vouchers: ${errorMessages}`);
                    }

                    logger.order('info', '🎫 ✅ ALL VOUCHERS UPDATED IN TRANSACTION', {
                        correlationId,
                        orderId: newOrder.id,
                        voucherCount: vouchersToUpdate.length
                    });
                }

                // Persist promotion audit trail
                if (promoResult.appliedPromos.length > 0) {
                    const orderItemMap = new Map(
                        newOrder.items.map(oi => [oi.cartItemId, oi.id])
                    );
                    // Use actual per-item discount from engine (not even split)
                    const itemDiscountMap = new Map(
                        promoResult.items.map(i => [i.cartItemId, i.promoDiscount ?? 0])
                    );
                    const promoAppliedData = promoResult.appliedPromos.flatMap(promo =>
                        promo.affectedItemIds
                            .filter(cartItemId => orderItemMap.has(cartItemId))
                            .map(cartItemId => ({
                                promotionId: promo.promotionId,
                                orderItemId: orderItemMap.get(cartItemId)!,
                                discountAmount: itemDiscountMap.get(cartItemId) ?? 0,
                            }))
                    );
                    if (promoAppliedData.length > 0) {
                        await tx.promotionAppliedItem.createMany({ data: promoAppliedData });
                        logger.order('info', '🏷️ ✅ PROMO AUDIT TRAIL SAVED', {
                            correlationId,
                            orderId: newOrder.id,
                            promoCount: promoResult.appliedPromos.length,
                            recordCount: promoAppliedData.length,
                        });
                    }
                }

                return newOrder;
            }, {
                maxWait: 5000,
                timeout: 10000,
            });

            // 7. Publish order created event (unified Redis + Socket.io)
            await this.publishOrderCreatedEvent(order as any);

            // 8. Emit voucher update for live UI updates
            if (appliedVouchers.length > 0) {
                await this.emitVoucherUpdate(user.id, merchantId, printerId);
            }

            // 9. Emit order-purchased event to user
            await this.socketService.emitToUser(user.id, SOCKET_EVENTS.ORDER.PURCHASED, {
                orderId: order.id,
                merchantId,
                printerId,
                total: order.total,
                itemCount: (order as any).items.length,
                state: order.state,
                paymentStatus: order.paymentStatus,
                createdAt: order.createdAt
            });

            // 10. Create order placeholder in chat
            try {
                await this.chatService.createOrderPlaceholder(user.id, merchantId, order.id);
                logger.order('info', 'Order placeholder created in chat', { correlationId, orderId: order.id });
            } catch (error) {
                logger.order('warn', 'Failed to create order placeholder in chat', {
                    correlationId,
                    orderId: order.id,
                    error: error instanceof Error ? error.message : String(error)
                });
                // Don't fail order creation if chat placeholder fails
            }

            logger.order('info', 'Cart order created successfully', {
                correlationId,
                userId: user.id,
                merchantId,
                printerId,
                orderId: order.id,
                itemCount: (order as any).items.length,
                subtotal: order.subtotal,
                voucherDiscount: order.voucherDiscount,
                total: order.total,
                duration: Date.now() - startTime
            });

            return order as Order & { items: OrderItem[] };
        } catch (error: any) {
            logger.order('error', 'Cart order creation failed', {
                correlationId,
                userId: user.id,
                merchantId,
                printerId,
                error: error.message,
                duration: Date.now() - startTime
            });
            throw error;
        }
    }

    /**
     * TODO: RESERVED FOR FUTURE PAYMENT FLOW
     *
     * This method is reserved for implementing full order payment in a single transaction.
     * Currently not used in production, but kept for future payment features.
     *
     * Use case: When implementing direct payment for complete orders (not split bills)
     *
     * @deprecated Consider using `updatePaymentStatus` for current payment operations
     */
    async payFullOrder(user: UserPayload, orderId: string) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Full order payment started', {
            correlationId,
            userId: user.id,
            orderId
        });

        try {
            const order = await this.prisma.order.findUnique({
                where: {
                    id: orderId,
                },
            });

            if (!order) {
                logger.order('warn', 'Payment failed - order not found', {
                    correlationId,
                    userId: user.id,
                    orderId
                });
                throw new Error("Order not found");
            }

            if (order.userId !== user.id) {
                logger.order('warn', 'Payment failed - user not order owner', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    orderOwnerId: order.userId
                });
                throw new Error("You are not the owner of the order");
            }

            const updatedOrder = await this.prisma.order.update({
                where: {
                    id: orderId,
                },
                data: {
                    paymentStatus: PaymentStatus.PAID,
                },
            });

            // Publish order updated event (unified Redis + Socket.io)
            await this.publishOrderUpdatedEvent(
                orderId,
                order.merchantId,
                OrderState.CLOSED,
                PaymentStatus.PAID,
                undefined,
                undefined,
                order.printerId,
                order.userId
            );

            // Award bonus points for order completion
            try {
                const bonusAwarded = await this.pointsRewardService.awardPointsForOrder(order, user);
                if (bonusAwarded) {
                    // Get points calculation for notification
                    const pointsCalculation = await this.pointsRewardService.calculatePointsForOrder(order, user);

                    if (pointsCalculation.shouldReward) {
                        // Send socket notification to the user who receives the points
                        const orderReward: PointsClaimedRewardPayload = {
                            id: `order-${order.id}`,
                            points: pointsCalculation.pointsAmount,
                            source: 'order_completion',
                            voucherTemplate: null,
                            surpriseBox: null,
                            battlePassTier: null,
                            challenge: null,
                        };
                        await this.socketService.emitToUser(
                            pointsCalculation.recipientUserId,
                            'points_claimed',
                            {
                                points: pointsCalculation.pointsAmount,
                                source: 'order_completion',
                                orderId: order.id,
                                orderTotal: order.total,
                                reason: pointsCalculation.reason,
                                rewards: [orderReward],
                            }
                        );

                        logger.order('info', 'Order completion bonus awarded', {
                            correlationId,
                            orderId,
                            recipientUserId: pointsCalculation.recipientUserId,
                            points: pointsCalculation.pointsAmount,
                            orderTotal: order.total
                        });
                    }
                }
            } catch (error: any) {
                logger.order('warn', 'Failed to award order completion bonus', {
                    correlationId,
                    orderId,
                    userId: user.id,
                    error: error.message
                });
                // Don't fail the order payment if bonus fails
            }

            logger.order('info', 'Full order payment completed', {
                correlationId,
                userId: user.id,
                orderId,
                orderTotal: order.total
            });

            return order;
        } catch (error: any) {
            logger.order('error', 'Full order payment failed', {
                correlationId,
                userId: user.id,
                orderId,
                error: error.message
            });
            throw error;
        }
    }

    /**
     * TODO: RESERVED FOR FUTURE SPLIT BILL FEATURE
     *
     * This method is reserved for implementing partial order payment where individual
     * order items can be paid separately (split bill functionality).
     * Currently not used in production, but kept for future payment features.
     *
     * Use case: When implementing split bill feature where group members pay for their own items
     *
     * @deprecated Consider using `updatePaymentStatus` for current payment operations
     */
    async payPartialOrder(user: UserPayload, orderId: string, orderItemsId: string[]) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Partial order payment started', {
            correlationId,
            userId: user.id,
            orderId,
            itemCount: orderItemsId.length
        });

        try {
            const updateResult = await this.prisma.orderItem.updateMany({
                where: {
                    id: {
                        in: orderItemsId,
                    },
                    ownerId: user.id,
                    paid: false,
                },
                data: {
                    paid: true,
                    paidAt: new Date(),
                    paidBy: user.id,
                },
            });

            logger.order('debug', 'Order items payment status updated', {
                correlationId,
                userId: user.id,
                orderId,
                updatedItemCount: updateResult.count,
                requestedItemCount: orderItemsId.length
            });
            const order = await this.prisma.order.findUnique({
                where: {
                    id: orderId,
                },
                include: {
                    items: true,
                },
            });
            if (!order) {
                throw new Error("Order not found");
            }

            let fullPaid = true;
            for (const item of order.items) {
                if (!item.paid) {
                    fullPaid = false;
                    break;
                }
            }

            const newPaymentStatus = fullPaid ? PaymentStatus.PAID : PaymentStatus.PARTIALLY_PAID;

            logger.order('debug', 'Determining final payment status', {
                correlationId,
                userId: user.id,
                orderId,
                totalItems: order.items.length,
                paidItems: order.items.filter(item => item.paid).length,
                fullPaid,
                newPaymentStatus
            });

            const updatedOrder = await this.prisma.order.update({
                where: {
                    id: orderId,
                },
                data: {
                    paymentStatus: newPaymentStatus,
                },
            });

            // Publish order updated event (unified Redis + Socket.io)
            await this.publishOrderUpdatedEvent(
                orderId,
                order.merchantId,
                OrderState.CLOSED,
                newPaymentStatus,
                undefined,
                undefined,
                order.printerId,
                order.userId
            );

            // Award bonus points only if order is fully paid
            if (newPaymentStatus === PaymentStatus.PAID) {
                try {
                    const bonusAwarded = await this.pointsRewardService.awardPointsForOrder(order, user);
                    if (bonusAwarded) {
                        // Get points calculation for notification
                        const pointsCalculation = await this.pointsRewardService.calculatePointsForOrder(order, user);

                        if (pointsCalculation.shouldReward) {
                            // Send socket notification to the user who receives the points
                            const orderReward: PointsClaimedRewardPayload = {
                                id: `order-${order.id}`,
                                points: pointsCalculation.pointsAmount,
                                source: 'order_completion',
                                voucherTemplate: null,
                                surpriseBox: null,
                                battlePassTier: null,
                                challenge: null,
                            };
                            await this.socketService.emitToUser(
                                pointsCalculation.recipientUserId,
                                'points_claimed',
                                {
                                    points: pointsCalculation.pointsAmount,
                                    source: 'order_completion',
                                    orderId: order.id,
                                    orderTotal: order.total,
                                    reason: pointsCalculation.reason,
                                    rewards: [orderReward],
                                }
                            );

                            logger.order('info', 'Order completion bonus awarded (partial payment)', {
                                correlationId,
                                orderId,
                                recipientUserId: pointsCalculation.recipientUserId,
                                points: pointsCalculation.pointsAmount,
                                orderTotal: order.total
                            });
                        }
                    }
                } catch (error: any) {
                    logger.order('warn', 'Failed to award order completion bonus (partial payment)', {
                        correlationId,
                        orderId,
                        userId: user.id,
                        error: error.message
                    });
                    // Don't fail the order payment if bonus fails
                }
            }

            logger.order('info', 'Partial order payment completed', {
                correlationId,
                userId: user.id,
                orderId,
                paymentStatus: newPaymentStatus,
                paidItemCount: order.items.filter(item => item.paid).length,
                totalItemCount: order.items.length
            });

            return updatedOrder;
        } catch (error: any) {
            logger.order('error', 'Partial order payment failed', {
                correlationId,
                userId: user.id,
                orderId,
                error: error.message
            });
            throw error;
        }
    }


    // Merchant order management methods
    async updateOrderStatus(user: UserPayload, orderId: string, merchantId: string, newStatus: OrderState, estimatedTime?: number) {
        const correlationId = logger.generateCorrelationId();
        const startTime = Date.now();

        logger.order('info', 'Updating order status started', {
            correlationId,
            userId: user.id,
            orderId,
            newStatus,
            estimatedTime
        });

        try {
            const order = await this.prisma.order.findFirst({
                where: { id: orderId, merchantId },
                include: {
                    items: true,
                    printer: true
                }
            });

            if (!order) {
                logger.order('warn', 'Order status update failed - order not found or unauthorized', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    merchantId
                });
                throw new NotFoundException("Order not found");
            }

            // Check if user has permission to update this order (merchant admin or CRM user)
            // This would be handled by auth guard, but we can add additional checks here

            const updateData: any = {
                state: newStatus,
                ...(estimatedTime && { estimatedTime }),
                ...(newStatus === OrderState.READY_FOR_PICKUP && { readyTime: new Date() }),
                // Auto-mark as PAID when closing an order that hasn't been paid yet
                ...(newStatus === OrderState.CLOSED && order.paymentStatus === PaymentStatus.PENDING && { paymentStatus: PaymentStatus.PAID }),
            };

            const updatedOrder = await this.prisma.order.update({
                where: { id: orderId },
                data: updateData,
                include: {
                    items: {
                        include: {
                            menuItem: true
                        }
                    },
                    appliedVoucher: {
                        include: {
                            template: true
                        }
                    },
                    printer: true
                }
            });

            // Handle voucher status update when order is completed
            if (newStatus === OrderState.CLOSED) {
                logger.order('info', '🎫 🏁 ORDER COMPLETED - MARKING ALL VOUCHERS AS USED', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    orderLevelVoucher: updatedOrder.appliedVoucher?.id,
                    orderItemsWithVouchers: updatedOrder.items.filter(item => item.appliedVoucherId).length
                });

                const vouchersToMarkAsUsed: string[] = [];

                // Order-level voucher (DISCOUNT vouchers)
                if (updatedOrder.appliedVoucher) {
                    vouchersToMarkAsUsed.push(updatedOrder.appliedVoucher.id);
                }

                // Item-level vouchers (FREE_ITEM vouchers)
                const itemVoucherIds = updatedOrder.items
                    .filter(item => item.appliedVoucherId)
                    .map(item => item.appliedVoucherId!);
                vouchersToMarkAsUsed.push(...itemVoucherIds);

                // Remove duplicates
                const uniqueVoucherIds = [...new Set(vouchersToMarkAsUsed)];

                logger.order('debug', '🎫 Vouchers to mark as USED', {
                    correlationId,
                    orderId,
                    orderLevelVoucherId: updatedOrder.appliedVoucher?.id,
                    itemLevelVoucherIds: itemVoucherIds,
                    allUniqueVoucherIds: uniqueVoucherIds,
                    totalVouchersToUpdate: uniqueVoucherIds.length
                });

                if (uniqueVoucherIds.length > 0) {
                    try {
                        const voucherUsedResults = await Promise.all(
                            uniqueVoucherIds.map(async (voucherId, index) => {
                                try {
                                    logger.order('debug', `🎫 Marking voucher ${index + 1}/${uniqueVoucherIds.length} as USED`, {
                                        correlationId,
                                        voucherId,
                                        orderId
                                    });

                                    await this.voucherApplicationService.markVoucherAsUsed(voucherId);
                                    return { success: true, voucherId };
                                } catch (error) {
                                    logger.order('error', '🎫 Failed to mark individual voucher as used', {
                                        correlationId,
                                        voucherId,
                                        orderId,
                                        error: error instanceof Error ? error.message : String(error)
                                    });
                                    return { success: false, voucherId, error };
                                }
                            })
                        );

                        const successCount = voucherUsedResults.filter(r => r.success).length;
                        const failureCount = voucherUsedResults.filter(r => !r.success).length;

                        logger.order('info', '🎫 📈 VOUCHER COMPLETION STATUS SUMMARY', {
                            correlationId,
                            userId: user.id,
                            orderId,
                            totalVouchers: uniqueVoucherIds.length,
                            successfullyMarkedAsUsed: successCount,
                            failedToMarkAsUsed: failureCount,
                            successfulVoucherIds: voucherUsedResults.filter(r => r.success).map(r => r.voucherId),
                            failedVoucherIds: voucherUsedResults.filter(r => !r.success).map(r => r.voucherId)
                        });
                    } catch (error) {
                        logger.order('error', '🎫 ❌ CRITICAL ERROR marking vouchers as used', {
                            correlationId,
                            userId: user.id,
                            orderId,
                            voucherIds: uniqueVoucherIds,
                            error: error instanceof Error ? error.message : String(error)
                        });
                        // Don't fail the order update if voucher update fails
                    }
                } else {
                    logger.order('debug', '🎫 No vouchers to mark as USED for this order', {
                        correlationId,
                        orderId
                    });
                }

                // Award bonus points if order is paid
                if (updatedOrder.paymentStatus === PaymentStatus.PAID) {
                    try {
                        logger.order('info', '💰 Awarding points for completed order', {
                            correlationId,
                            orderId,
                            userId: updatedOrder.userId ?? undefined,
                            paymentStatus: updatedOrder.paymentStatus
                        });

                        const bonusAwarded = await this.pointsRewardService.awardPointsForOrder(updatedOrder, user);
                        if (bonusAwarded) {
                            const pointsCalculation = await this.pointsRewardService.calculatePointsForOrder(updatedOrder, user);
                            if (pointsCalculation.shouldReward) {
                                const orderReward: PointsClaimedRewardPayload = {
                                    id: `order-${updatedOrder.id}`,
                                    points: pointsCalculation.pointsAmount,
                                    source: 'order_completion',
                                    voucherTemplate: null,
                                    surpriseBox: null,
                                    battlePassTier: null,
                                    challenge: null,
                                };
                                await this.socketService.emitToUser(
                                    pointsCalculation.recipientUserId,
                                    'points_claimed',
                                    {
                                        points: pointsCalculation.pointsAmount,
                                        source: 'order_completion',
                                        orderId: updatedOrder.id,
                                        orderTotal: updatedOrder.total,
                                        reason: pointsCalculation.reason,
                                        rewards: [orderReward],
                                    }
                                );

                                logger.order('info', '💰 Points awarded successfully', {
                                    correlationId,
                                    orderId,
                                    recipientUserId: pointsCalculation.recipientUserId,
                                    pointsAwarded: pointsCalculation.pointsAmount,
                                    orderTotal: updatedOrder.total
                                });
                            }
                        }
                    } catch (error) {
                        logger.order('error', '💰 Failed to award points for order', {
                            correlationId,
                            orderId,
                            error: error instanceof Error ? error.message : String(error)
                        });
                        // Don't fail the order update if points awarding fails
                    }
                }
            }

            // Publish order updated event (unified Redis + Socket.io)
            await this.publishOrderUpdatedEvent(
                orderId,
                updatedOrder.merchantId,
                newStatus,
                updatedOrder.paymentStatus,
                updatedOrder.estimatedTime || undefined,
                updatedOrder.readyTime || undefined,
                updatedOrder.printerId,
                updatedOrder.userId
            );

            // Send push notification for order status changes
            await this.sendOrderStatusNotification(updatedOrder, newStatus);

            // Update chat order placeholder with new status
            if (updatedOrder.userId && updatedOrder.merchantId) {
                try {
                    await this.chatService.notifyOrderStatusChange(orderId, newStatus);
                } catch (error) {
                    logger.order('warn', 'Failed to update chat order placeholder', {
                        correlationId,
                        orderId,
                        newStatus,
                        error: error instanceof Error ? error.message : String(error)
                    });
                    // Don't fail the order update if chat notification fails
                }

                // Create a SYSTEM message in chat for meaningful status changes
                const meaningfulStatuses = [
                    OrderState.PREPARING,
                    OrderState.READY_FOR_PICKUP,
                    OrderState.CLOSED,
                    OrderState.CANCELLED
                ];
                if (meaningfulStatuses.includes(newStatus)) {
                    try {
                        await this.chatService.createOrderStatusMessage(
                            updatedOrder.userId,
                            updatedOrder.merchantId,
                            orderId,
                            newStatus
                        );
                    } catch (error) {
                        logger.order('warn', 'Failed to create chat order status message', {
                            correlationId,
                            orderId,
                            newStatus,
                            error: error instanceof Error ? error.message : String(error)
                        });
                    }
                }
            }

            // Send push notification to merchant staff when order is ready for pickup
            if (newStatus === OrderState.READY_FOR_PICKUP && updatedOrder.merchantId) {
                await this.notificationService.sendMerchantStaffNotification(
                    updatedOrder.merchantId,
                    orderId,
                    newStatus,
                    {
                        id: updatedOrder.printerId ?? 0,
                        name: updatedOrder.printer?.name ?? 'Table'
                    }
                );
            }

            logger.order('info', 'Order status updated successfully', {
                correlationId,
                userId: user.id,
                orderId,
                previousStatus: order.state,
                newStatus,
                estimatedTime,
                duration: Date.now() - startTime
            });

            return updatedOrder;
        } catch (error: any) {
            logger.order('error', 'Order status update failed', {
                correlationId,
                userId: user.id,
                orderId,
                newStatus,
                error: error.message,
                duration: Date.now() - startTime
            });
            throw error;
        }
    }

    /**
     * Set warning flag on order (kitchen can flag issues for waiter attention)
     */
    async setOrderWarning(orderId: string, hasWarning: boolean) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Setting order warning', {
            correlationId,
            orderId,
            hasWarning
        });

        const order = await this.prisma.order.findUnique({
            where: { id: orderId }
        });

        if (!order) {
            throw new Error("Order not found");
        }

        const updatedOrder = await this.prisma.order.update({
            where: { id: orderId },
            data: { hasWarning },
            include: {
                items: {
                    include: {
                        menuItem: true
                    }
                },
                printer: true
            }
        });

        // Emit real-time update via WebSocket
        await this.publishOrderUpdatedEvent(
            orderId,
            updatedOrder.merchantId,
            updatedOrder.state,
            updatedOrder.paymentStatus,
            updatedOrder.estimatedTime || undefined,
            updatedOrder.readyTime || undefined,
            updatedOrder.printerId,
            updatedOrder.userId
        );

        logger.order('info', 'Order warning set successfully', {
            correlationId,
            orderId,
            hasWarning
        });

        return updatedOrder;
    }

    async updatePaymentStatus(user: UserPayload, orderId: string, newPaymentStatus: PaymentStatus) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Updating payment status started', {
            correlationId,
            userId: user.id,
            orderId,
            newPaymentStatus
        });

        try {
            const order = await this.prisma.order.findUnique({
                where: { id: orderId },
                include: { items: true }
            });

            if (!order) {
                logger.order('warn', 'Payment status update failed - order not found', {
                    correlationId,
                    userId: user.id,
                    orderId
                });
                throw new Error("Order not found");
            }

            const updatedOrder = await this.prisma.order.update({
                where: { id: orderId },
                data: { paymentStatus: newPaymentStatus },
                include: {
                    items: {
                        include: {
                            menuItem: true
                        }
                    },
                    printer: true
                }
            });

            // Publish order updated event (unified Redis + Socket.io)
            await this.publishOrderUpdatedEvent(
                orderId,
                updatedOrder.merchantId,
                updatedOrder.state,
                newPaymentStatus,
                updatedOrder.estimatedTime || undefined,
                updatedOrder.readyTime || undefined,
                updatedOrder.printerId,
                updatedOrder.userId
            );

            // Award bonus points if order is already closed and now paid
            // This handles the case where merchant closes order first, then marks as paid
            if (newPaymentStatus === PaymentStatus.PAID && updatedOrder.state === OrderState.CLOSED) {
                try {
                    logger.order('info', '💰 Awarding points for paid order (already closed)', {
                        correlationId,
                        orderId,
                        userId: updatedOrder.userId ?? undefined,
                        orderState: updatedOrder.state
                    });

                    const bonusAwarded = await this.pointsRewardService.awardPointsForOrder(updatedOrder, user);
                    if (bonusAwarded) {
                        const pointsCalculation = await this.pointsRewardService.calculatePointsForOrder(updatedOrder, user);
                        if (pointsCalculation.shouldReward) {
                            const orderReward: PointsClaimedRewardPayload = {
                                id: `order-${updatedOrder.id}`,
                                points: pointsCalculation.pointsAmount,
                                source: 'order_completion',
                                voucherTemplate: null,
                                surpriseBox: null,
                                battlePassTier: null,
                                challenge: null,
                            };
                            await this.socketService.emitToUser(
                                pointsCalculation.recipientUserId,
                                'points_claimed',
                                {
                                    points: pointsCalculation.pointsAmount,
                                    source: 'order_completion',
                                    orderId: updatedOrder.id,
                                    orderTotal: updatedOrder.total,
                                    reason: pointsCalculation.reason,
                                    rewards: [orderReward],
                                }
                            );

                            logger.order('info', '💰 Points awarded successfully', {
                                correlationId,
                                orderId,
                                recipientUserId: pointsCalculation.recipientUserId,
                                pointsAwarded: pointsCalculation.pointsAmount,
                                orderTotal: updatedOrder.total
                            });
                        }
                    }
                } catch (error) {
                    logger.order('error', '💰 Failed to award points for order', {
                        correlationId,
                        orderId,
                        error: error instanceof Error ? error.message : String(error)
                    });
                    // Don't fail the payment status update if points awarding fails
                }
            }

            logger.order('info', 'Payment status updated successfully', {
                correlationId,
                userId: user.id,
                orderId,
                previousStatus: order.paymentStatus,
                newPaymentStatus
            });

            return updatedOrder;
        } catch (error: any) {
            logger.order('error', 'Payment status update failed', {
                correlationId,
                userId: user.id,
                orderId,
                newPaymentStatus,
                error: error.message
            });
            throw error;
        }
    }

    async getOrdersForMerchant(merchantId: string, status?: OrderState, limit: number = 50) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Fetching merchant orders', {
            correlationId,
            merchantId,
            status,
            limit
        });

        try {
            const whereCondition: any = {
                merchantId,
                ...(status && { state: status })
            };

            const orders = await this.prisma.order.findMany({
                where: whereCondition,
                include: {
                    items: {
                        include: {
                            menuItem: true,
                            appliedVoucher: {
                                include: {
                                    template: true,
                                },
                            },
                        }
                    },
                    printer: true,
                    appliedVoucher: {
                        include: {
                            template: true,
                        },
                    },
                },
                orderBy: { createdAt: 'desc' },
                take: limit
            });

            logger.order('info', 'Merchant orders fetched successfully', {
                correlationId,
                merchantId,
                orderCount: orders.length,
                status
            });

            return orders;
        } catch (error: any) {
            logger.order('error', 'Failed to fetch merchant orders', {
                correlationId,
                merchantId,
                status,
                error: error.message
            });
            throw error;
        }
    }

    async getOrderById(orderId: string, merchantId: string) {
        const correlationId = logger.generateCorrelationId();

        try {
            const order = await this.prisma.order.findFirst({
                where: { id: orderId, merchantId },
                include: {
                    items: {
                        include: {
                            menuItem: true
                        }
                    },
                    printer: true
                }
            });

            if (!order) {
                logger.order('warn', 'Order not found or unauthorized', {
                    correlationId,
                    orderId,
                    merchantId
                });
                throw new NotFoundException("Order not found");
            }

            return order;
        } catch (error: any) {
            logger.order('error', 'Failed to get order by ID', {
                correlationId,
                orderId,
                error: error.message
            });
            throw error;
        }
    }

    async cancelOrder(user: UserPayload, orderId: string, merchantId: string, reason?: string) {
        const correlationId = logger.generateCorrelationId();

        logger.order('info', 'Cancelling order', {
            correlationId,
            userId: user.id,
            orderId,
            reason
        });

        try {
            const order = await this.prisma.order.findFirst({
                where: { id: orderId, merchantId },
                include: {
                    items: true,
                    appliedVoucher: {
                        include: {
                            template: true
                        }
                    }
                }
            });

            if (!order) {
                logger.order('warn', 'Order cancellation failed - order not found or unauthorized', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    merchantId
                });
                throw new NotFoundException("Order not found");
            }

            // Use state machine to validate cancellation
            const stateMachine = OrderStateMachine.from(
                order.state,
                order.deliveryMode,
                order.paymentStatus
            );

            if (!stateMachine.canBeCancelled()) {
                logger.order('warn', 'Order cancellation failed - order cannot be cancelled', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    currentStatus: order.state
                });
                throw new Error(`Cannot cancel order in ${order.state} state`);
            }

            // Return voucher to available if applied
            if (order.appliedVoucher) {
                await this.voucherApplicationService.returnVoucherToAvailable(order.appliedVoucher.id);

                logger.order('info', 'Voucher returned to available status', {
                    correlationId,
                    userId: user.id,
                    orderId,
                    userVoucherId: order.appliedVoucher.id
                });
            }

            const updatedOrder = await this.prisma.order.update({
                where: { id: orderId },
                data: {
                    state: OrderState.CANCELLED,
                    paymentStatus: PaymentStatus.CANCELLED
                },
                include: {
                    items: {
                        include: {
                            menuItem: true
                        }
                    },
                    appliedVoucher: {
                        include: {
                            template: true
                        }
                    },
                    printer: true
                }
            });

            // Publish order cancelled event (unified Redis + Socket.io)
            await this.publishOrderCancelledEvent(
                orderId,
                updatedOrder.merchantId,
                OrderState.CANCELLED,
                PaymentStatus.CANCELLED,
                updatedOrder.printerId,
                updatedOrder.userId
            );

            // Emit voucher update for live UI updates
            if (order.appliedVoucher) {
                await this.emitVoucherUpdate(
                    user.id,
                    order.merchantId,
                    order.printerId?.toString()
                );
            }

            logger.order('info', 'Order cancelled successfully', {
                correlationId,
                userId: user.id,
                orderId,
                previousStatus: order.state,
                reason,
                voucherReturned: !!order.appliedVoucher
            });

            return updatedOrder;
        } catch (error: any) {
            logger.order('error', 'Order cancellation failed', {
                correlationId,
                userId: user.id,
                orderId,
                reason,
                error: error.message
            });
            throw error;
        }
    }

    /**
     * Sends push notification for order status changes
     */
    private async sendOrderStatusNotification(order: any, newStatus: OrderState) {
        try {
            // Get merchant details for notification
            const merchant = await this.prisma.merchant.findUnique({
                where: { id: order.merchantId },
                select: { name: true }
            });

            if (!merchant) {
                logger.order('warn', 'Merchant not found for order notification', {
                    orderId: order.id,
                    merchantId: order.merchantId
                });
                return;
            }

            // Get user details
            const user = await this.prisma.user.findUnique({
                where: { id: order.userId },
                select: { id: true, username: true }
            });

            if (!user) {
                logger.order('warn', 'User not found for order notification', {
                    orderId: order.id,
                    userId: order.userId
                });
                return;
            }

            // Send notification for specific status changes
            const statusesToNotify = [
                OrderState.PREPARING,
                OrderState.READY_FOR_PICKUP,
                OrderState.CLOSED, // When marked as delivered
                OrderState.CANCELLED // When order is cancelled
            ];

            if (statusesToNotify.includes(newStatus)) {
                // Check if user is currently in the chat - skip push if they are
                const isUserInChat = await this.chatService.isUserInMerchantChat(
                    user.id,
                    order.merchantId
                );

                if (isUserInChat) {
                    logger.order('info', 'Skipping order status notification - user is in chat', {
                        orderId: order.id,
                        userId: user.id,
                        status: newStatus,
                        merchantId: order.merchantId
                    });
                } else {
                    await this.notificationService.sendOrderStatusNotification(
                        user.id,
                        order.id,
                        newStatus,
                        merchant.name,
                        order.merchantId
                    );

                    logger.order('info', 'Order status notification sent', {
                        orderId: order.id,
                        userId: user.id,
                        status: newStatus,
                        merchantId: order.merchantId,
                        merchantName: merchant.name
                    });
                }
            }
        } catch (error: any) {
            logger.order('error', 'Failed to send order status notification', {
                orderId: order.id,
                newStatus,
                error: error.message
            });
        }
    }

    /**
     * Emits voucher update events for live UI updates
     */
    private async emitVoucherUpdate(userId: string, merchantId: string, printerId?: string) {
        try {
            Logger.info('🎫 📡 EMITTING VOUCHER UPDATE SOCKET EVENTS', {
                userId,
                merchantId,
                printerId,
                action: 'emitVoucherUpdate',
                timestamp: new Date().toISOString()
            });

            // Fetch current user vouchers
            const userVouchers = await this.voucherApplicationService.getUserAvailableVouchers(userId, merchantId);

            Logger.debug('🎫 Current available vouchers after update', {
                userId,
                merchantId,
                availableVoucherCount: userVouchers.length,
                availableVouchers: userVouchers.map(v => ({
                    id: v.id,
                    name: v.template?.name,
                    type: v.template?.type,
                    status: v.status,
                    expiresAt: v.expiresAt
                }))
            });

            const voucherUpdateEvent = {
                userId,
                merchantId,
                printerId,
                vouchers: userVouchers,
                action: 'UPDATED' as const,
                timestamp: new Date().toISOString(),
                voucherCount: userVouchers.length
            };

            // Emit to user
            Logger.debug('🎫 📤 Emitting voucher_update to user', {
                userId,
                eventType: 'voucher_update',
                voucherCount: userVouchers.length
            });
            await this.socketService.emitToUser(userId, 'voucher_update', voucherUpdateEvent);

            // Emit to lobby if in lobby
            if (printerId) {
                Logger.debug('🎫 📤 Emitting lobby_voucher_update to lobby', {
                    printerId,
                    userId,
                    eventType: 'lobby_voucher_update',
                    voucherCount: userVouchers.length
                });
                await this.socketService.emitToLobby(printerId, 'lobby_voucher_update', voucherUpdateEvent);
            }

            // Emit to merchant
            Logger.debug('🎫 📤 Emitting merchant_voucher_update to merchant', {
                merchantId,
                userId,
                eventType: 'merchant_voucher_update',
                voucherCount: userVouchers.length
            });
            await this.socketService.emitToMerchant(merchantId, 'merchant_voucher_update', voucherUpdateEvent);

            Logger.info('🎫 ✅ ALL VOUCHER UPDATE SOCKET EVENTS EMITTED', {
                userId,
                merchantId,
                printerId,
                eventsEmitted: [
                    'voucher_update (to user)',
                    printerId ? 'lobby_voucher_update (to lobby)' : 'none (no lobby)',
                    'merchant_voucher_update (to merchant)'
                ].filter(event => !event.includes('none')),
                voucherCount: userVouchers.length
            });
        } catch (error: any) {
            Logger.error('🎫 ❌ FAILED TO EMIT VOUCHER UPDATE', {
                userId,
                merchantId,
                printerId,
                error: error.message,
                stack: error.stack
            });
        }
    }

    /**
     * UNIFIED EVENT PUBLISHING METHODS
     *
     * Publishes events to Redis only. Socket.io emission is handled by OrderPubSubService
     * which subscribes to Redis patterns and emits to Socket.io rooms.
     * This prevents duplicate event emission.
     */

    /**
     * Publish order created event to Redis
     * Socket.io emission is handled by OrderPubSubService
     */
    private async publishOrderCreatedEvent(order: Order & { items: OrderItem[] }): Promise<void> {
        try {
            await this.orderRedisService.publishOrderCreated(order);
            logger.order('debug', '✅ Order created event published to Redis', {
                orderId: order.id,
                merchantId: order.merchantId
            });
        } catch (error: any) {
            logger.order('error', '⚠️  ORDER CREATED EVENT PUBLISHING FAILED', {
                orderId: order.id,
                merchantId: order.merchantId,
                error: error?.message || 'Unknown error'
            });
        }
    }

    /**
     * Publish order updated event to Redis
     * Socket.io emission is handled by OrderPubSubService
     */
    private async publishOrderUpdatedEvent(
        orderId: string,
        merchantId: string,
        state: OrderState,
        paymentStatus: PaymentStatus,
        estimatedTime?: number,
        readyTime?: Date,
        // Lobby specification v2 fields
        printerId?: number | null,
        userId?: string | null
    ): Promise<void> {
        try {
            await this.orderRedisService.publishOrderUpdated(
                orderId,
                merchantId,
                state,
                paymentStatus,
                estimatedTime,
                readyTime
            );
            logger.order('debug', '✅ Order updated event published to Redis', {
                orderId,
                merchantId,
                state
            });
        } catch (error: any) {
            logger.order('error', '⚠️  ORDER UPDATED EVENT PUBLISHING FAILED', {
                orderId,
                merchantId,
                state,
                error: error?.message || 'Unknown error'
            });
        }
    }

    /**
     * Publish order cancelled event to Redis
     * Socket.io emission is handled by OrderPubSubService
     */
    private async publishOrderCancelledEvent(
        orderId: string,
        merchantId: string,
        state: OrderState,
        paymentStatus: PaymentStatus,
        // Lobby specification v2 fields
        printerId?: number | null,
        userId?: string | null
    ): Promise<void> {
        try {
            await this.orderRedisService.publishOrderCancelled(orderId, merchantId);
            logger.order('debug', '✅ Order cancelled event published to Redis', {
                orderId,
                merchantId
            });
        } catch (error: any) {
            logger.order('error', '⚠️  ORDER CANCELLED EVENT PUBLISHING FAILED', {
                orderId,
                merchantId,
                error: error?.message || 'Unknown error'
            });
        }
    }

    // ============================================
    // STATION-BASED KITCHEN WORKFLOW (ITEM STATE)
    // ============================================

    /**
     * Update the state of a single order item
     * @param orderId - The order ID
     * @param itemId - The order item ID
     * @param state - The new state (PENDING or READY)
     * @param changedBy - User ID who made the change
     * @returns Updated order with all items and allItemsReady flag
     */
    async updateOrderItemState(
        orderId: string,
        itemId: string,
        state: OrderItemState,
        changedBy: string
    ): Promise<{ order: Order & { items: OrderItem[] }; allItemsReady: boolean }> {
        logger.order('info', '🍳 Updating order item state', {
            orderId,
            itemId,
            newState: state,
            changedBy
        });

        // Verify item exists and belongs to order
        const item = await this.prisma.orderItem.findFirst({
            where: { id: itemId, orderId }
        });

        if (!item) {
            throw new Error(`Order item ${itemId} not found in order ${orderId}`);
        }

        // Update the item state
        await this.prisma.orderItem.update({
            where: { id: itemId },
            data: {
                itemState: state,
                itemStateAt: new Date(),
                itemStateBy: changedBy
            }
        });

        // Fetch updated order with all items
        const order = await this.prisma.order.findUnique({
            where: { id: orderId },
            include: { items: true }
        });

        if (!order) {
            throw new Error(`Order ${orderId} not found`);
        }

        const allItemsReady = this.checkAllItemsReady(order.items);

        // Publish item state change event
        await this.publishOrderItemStateChangedEvent(orderId, order.merchantId, [itemId], state, allItemsReady, order.printerId);

        logger.order('info', '🍳 Order item state updated', {
            orderId,
            itemId,
            newState: state,
            allItemsReady
        });

        return { order, allItemsReady };
    }

    /**
     * Update the state of multiple order items (bulk action)
     * @param orderId - The order ID
     * @param itemIds - Array of order item IDs
     * @param state - The new state (PENDING or READY)
     * @param changedBy - User ID who made the change
     * @returns Updated order with all items and allItemsReady flag
     */
    async updateOrderItemsState(
        orderId: string,
        itemIds: string[],
        state: OrderItemState,
        changedBy: string
    ): Promise<{ order: Order & { items: OrderItem[] }; allItemsReady: boolean }> {
        logger.order('info', '🍳 Bulk updating order item states', {
            orderId,
            itemIds,
            newState: state,
            changedBy
        });

        // Verify all items exist and belong to order
        const items = await this.prisma.orderItem.findMany({
            where: { id: { in: itemIds }, orderId }
        });

        if (items.length !== itemIds.length) {
            const foundIds = items.map(i => i.id);
            const missingIds = itemIds.filter(id => !foundIds.includes(id));
            throw new Error(`Order items not found: ${missingIds.join(', ')}`);
        }

        // Bulk update item states
        await this.prisma.orderItem.updateMany({
            where: { id: { in: itemIds }, orderId },
            data: {
                itemState: state,
                itemStateAt: new Date(),
                itemStateBy: changedBy
            }
        });

        // Fetch updated order with all items
        const order = await this.prisma.order.findUnique({
            where: { id: orderId },
            include: { items: true }
        });

        if (!order) {
            throw new Error(`Order ${orderId} not found`);
        }

        const allItemsReady = this.checkAllItemsReady(order.items);

        // Publish bulk item state change event
        await this.publishOrderItemStateChangedEvent(orderId, order.merchantId, itemIds, state, allItemsReady, order.printerId);

        logger.order('info', '🍳 Bulk order item states updated', {
            orderId,
            itemCount: itemIds.length,
            newState: state,
            allItemsReady
        });

        return { order, allItemsReady };
    }

    /**
     * Check if all items in an order are READY
     * Used for REQUIRE_ALL_ITEMS_READY guard
     */
    checkAllItemsReady(items: OrderItem[]): boolean {
        return items.every(item => item.itemState === OrderItemState.READY);
    }

    /**
     * Get order with items and check if all items are ready
     * Used by GraphQL resolver for allItemsReady computed field
     */
    async getOrderAllItemsReady(orderId: string): Promise<boolean> {
        const order = await this.prisma.order.findUnique({
            where: { id: orderId },
            include: { items: true }
        });

        if (!order) {
            return false;
        }

        return this.checkAllItemsReady(order.items);
    }

    /**
     * Publish order item state changed event
     * Emits to merchant room for CRM updates
     */
    private async publishOrderItemStateChangedEvent(
        orderId: string,
        merchantId: string,
        itemIds: string[],
        state: OrderItemState,
        allItemsReady: boolean,
        printerId?: number | null
    ): Promise<void> {
        try {
            const eventData = {
                orderId,
                merchantId,
                itemIds,
                newState: state,
                allItemsReady,
                printerId
            };

            const eventName = itemIds.length === 1 ? 'order_item:state_changed' : 'order_item:bulk_state_changed';

            // Emit to merchant room for CRM clients
            await this.socketService.emitToMerchant(merchantId, eventName, eventData);

            logger.order('debug', '✅ Order item state change event published', {
                orderId,
                itemIds,
                state,
                allItemsReady
            });
        } catch (error: any) {
            logger.order('error', '⚠️  ORDER ITEM STATE CHANGE EVENT PUBLISHING FAILED', {
                orderId,
                itemIds,
                state,
                error: error?.message || 'Unknown error'
            });
        }
    }
}// force restart
