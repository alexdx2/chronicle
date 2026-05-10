import { Server, Socket } from "socket.io";
import { BaseSocket } from "./base.socket";
import { UserPayload } from "../models/user.model";
import { PrismaClient } from "@prisma/client";
import { RedisClientType } from "redis";
import Logger from "../logger";
import { logger } from "../logger";
import { RefactoredPrinterFactory } from "../services/refactored-printer.service";
import * as uuid from 'uuid';
import { PrinterModel } from "../models/printer.model";
import { CreateOrder } from "../services/order.service";
enum ClientSocketEvent {
    ACTIVATE_VOUCHER = "activate_voucher",
    DEACTIVATE_VOUCHER = "deactivate_voucher",
    DISCONNECT_PRINTER = "disconnect_printer",
    DISCONNECTING = "disconnecting",
    CREATE_GROUP = "create_group",
    JOIN_GROUP = "join_group",
    LEAVE_GROUP = "leave_group",
    INVITE_TO_GROUP = "invite_to_group",
    DECLINE_GROUP_INVITE = "decline_group_invite",
    GENERATE_GROUP_INVITE = "generate_group_invite",
    JOIN_GROUP_BY_INVITE = "join_group_by_invite",
    CHANGE_CONTEXT = "change_context",

    MAKE_ORDER = "make_order",

}

enum ClientSocketOutputEvent {
    VOUCHER_CLAIMED = "voucher_claimed",
    POINTS_CLAIMED = "points_claimed",
    JOINED_PRINTER = "joined_printer",
}

class ClientSocket extends BaseSocket {
    constructor(socket: Socket, server: Server, user: UserPayload, prisma: PrismaClient, redis: RedisClientType, printerFactory: RefactoredPrinterFactory) {
        super(socket, server, user, prisma, redis, printerFactory);
    }
    handlers: { [key: string]: (...args: any[]) => void | Promise<void> | Promise<string | undefined>; } = {
        [ClientSocketEvent.ACTIVATE_VOUCHER]: this.activateVoucher,
        [ClientSocketEvent.DEACTIVATE_VOUCHER]: this.deactivateVoucher,
        [ClientSocketEvent.DISCONNECT_PRINTER]: this.disconnectPrinter,
        [ClientSocketEvent.DISCONNECTING]: this.disconnecting,
        [ClientSocketEvent.CREATE_GROUP]: this.createGroup,
        [ClientSocketEvent.JOIN_GROUP]: this.joinGroup,
        [ClientSocketEvent.LEAVE_GROUP]: this.leaveGroup,
        [ClientSocketEvent.INVITE_TO_GROUP]: this.inviteToGroup,
        [ClientSocketEvent.DECLINE_GROUP_INVITE]: this.declineGroupInvite,
        [ClientSocketEvent.GENERATE_GROUP_INVITE]: this.generateGroupInvite,
        [ClientSocketEvent.JOIN_GROUP_BY_INVITE]: this.joinGroupByInvite,
        [ClientSocketEvent.CHANGE_CONTEXT]: this.changeContext,
        [ClientSocketEvent.MAKE_ORDER]: this.makeOrder,
    };

    async joinPrinter(printerId: string) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', 'Client attempting to join printer', {
            correlationId,
            userId: this.user.id,
            printerId,
            socketId: this.socket.id
        });

        try {
            const printer = await this.getPrinter(parseInt(printerId));
            const printerService = await this.getPrinterService(printer);
            await printerService.beforeUserJoinPrinter(this.user);
            super.ensureOnePrinterRoom(true);

            const roomName = PrinterModel.getClientRoom(printer.entity.id);

            this.socket.join(roomName);

            logger.socket('debug', 'Client joined printer room', {
                correlationId,
                userId: this.user.id,
                printerId,
                roomName,
                socketId: this.socket.id
            });

            super.emitOnPrinter(printer, ClientSocketOutputEvent.JOINED_PRINTER, printerId);
            await printerService.onUserJoinPrinter(this.user);
            await BaseSocket.emitStateChanged(printerService, this.server);

            logger.socket('info', 'Client successfully joined printer and state updated', {
                correlationId,
                userId: this.user.id,
                printerId,
                merchantId: printer.entity.merchantId
            });

        } catch (error: any) {
            logger.socket('error', 'Failed to join printer room', {
                correlationId,
                userId: this.user.id,
                printerId,
                error: error.message,
                socketId: this.socket.id
            });
            // Emit error to the client so they know the join failed
            this.socket.emit('error', { message: 'Failed to join printer', error: error.message });
            throw error;
        }
    }

    async activateVoucher(voucherId: string) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', 'Voucher activation request', {
            correlationId,
            userId: this.user.id,
            voucherId,
            socketId: this.socket.id
        });

        try {
            const printerService = await this.getPrinterService();
            const result = await printerService.ActivateVoucher(voucherId, this.user);

            if (!result.isSuccess) {
                logger.socket('warn', '🎫 ❌ Socket voucher activation failed', {
                    correlationId,
                    userId: this.user.id,
                    voucherId,
                    error: result.error,
                    socketId: this.socket.id
                });

                // Emit failure acknowledgment
                this.socket.emit('voucher-activated', { success: false, voucherId, error: result.error });
                return;
            }

            logger.socket('info', '🎫 ✅ Socket voucher activation successful', {
                correlationId,
                userId: this.user.id,
                voucherId,
                socketId: this.socket.id
            });

            // Emit success acknowledgment to the user
            this.socket.emit('voucher-activated', { success: true, voucherId });

            // Emit state change to all users
            await BaseSocket.emitStateChanged(printerService, this.server);
        } catch (error: any) {
            logger.socket('error', 'Voucher activation error', {
                correlationId,
                userId: this.user.id,
                voucherId,
                error: error.message,
                socketId: this.socket.id
            });
            this.socket.emit('voucher-activated', { success: false, voucherId, error: error.message });
        }
    }

    async deactivateVoucher(uid: string) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', '🎫 ❌ Socket voucher deactivation request', {
            correlationId,
            userId: this.user.id,
            voucherUid: uid,
            socketId: this.socket.id
        });

        try {
            const printerService = await this.getPrinterService();
            const result = await printerService.DeactivateVoucher(uid, this.user);

            if (!result.isSuccess) {
                logger.socket('warn', '🎫 ❌ Socket voucher deactivation failed', {
                    correlationId,
                    userId: this.user.id,
                    voucherUid: uid,
                    error: result.error,
                    socketId: this.socket.id
                });

                // Emit failure acknowledgment
                this.socket.emit('voucher_deactivated', { success: false, voucherUid: uid, error: result.error });
                return;
            }

            logger.socket('info', '🎫 ✅ Socket voucher deactivation successful', {
                correlationId,
                userId: this.user.id,
                voucherUid: uid,
                socketId: this.socket.id
            });

            // Emit success acknowledgment to the user
            this.socket.emit('voucher_deactivated', { success: true, voucherUid: uid });

            // Emit state change to all users
            await BaseSocket.emitStateChanged(printerService, this.server);
        } catch (error: any) {
            logger.socket('error', 'Voucher deactivation error', {
                correlationId,
                userId: this.user.id,
                voucherUid: uid,
                error: error.message,
                socketId: this.socket.id
            });
            this.socket.emit('voucher_deactivated', { success: false, voucherUid: uid, error: error.message });
        }
    }

    async disconnectPrinter() {
        try {
            const printerService = await this.getPrinterService();
            await printerService.UserLeavePrinter(this.user);
            await BaseSocket.emitStateChanged(printerService, this.server);
        } catch (error: any) {
            // Gracefully handle case where user was not in a printer room
            Logger.debug(`Disconnect printer skipped: ${error.message}`);
        }
    }

    async disconnecting() {
        try {
            const printerService = await this.getPrinterService();
            // Use handleUserDisconnect instead of UserLeavePrinter to preserve
            // user context, vouchers, and group membership for reconnection scenarios.
            // Redis TTL handles automatic cleanup of stale data.
            await printerService.handleUserDisconnect(this.user);
            Logger.debug("Disconnecting client (context preserved for reconnect)");
            await BaseSocket.emitStateChanged(printerService, this.server);
        } catch (error: any) {
            // Gracefully handle case where user was not in a printer room
            // This can happen during test cleanup or if user disconnects before joining
            Logger.debug(`Disconnect cleanup skipped: ${error.message}`);
        }
    }

    async createGroup() {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', 'Group creation request', {
            correlationId,
            userId: this.user.id,
            socketId: this.socket.id
        });

        try {
            const printerService = await this.getPrinterService();
            const result = await printerService.createGroup(this.user);

            await BaseSocket.emitStateChanged(printerService, this.server);

            // Emit acknowledgment to the client who created the group
            this.socket.emit('group_created', { success: true, groupId: result.data });

            logger.socket('info', 'Group created successfully', {
                correlationId,
                userId: this.user.id,
                socketId: this.socket.id
            });
        } catch (error: any) {
            logger.socket('error', 'Group creation failed', {
                correlationId,
                userId: this.user.id,
                error: error.message,
                socketId: this.socket.id
            });
            this.socket.emit('group_created', { success: false, error: error.message });
        }
    }

    async joinGroup(groupId: string) {
        try {
            const printerService = await this.getPrinterService();
            await printerService.joinGroup(groupId, this.user);

            await BaseSocket.emitStateChanged(printerService, this.server);
            this.socket.emit('joined_group', { success: true, groupId });
        } catch (error: any) {
            this.socket.emit('joined_group', { success: false, error: error.message });
        }
    }

    async leaveGroup(groupId: string) {
        try {
            const printerService = await this.getPrinterService();
            await printerService.leaveGroup(this.user);

            await BaseSocket.emitStateChanged(printerService, this.server);
            this.socket.emit('left_group', { success: true });
        } catch (error: any) {
            this.socket.emit('left_group', { success: false, error: error.message });
        }
    }

    async inviteToGroup(data: { groupId: string, userId: string } | string, userId?: string) {
        // Handle both old and new parameter formats for backward compatibility
        let groupId: string;
        let targetUserId: string;

        if (typeof data === 'object') {
            groupId = data.groupId;
            targetUserId = data.userId;
        } else {
            groupId = data;
            targetUserId = userId!;
        }

        try {
            const printerService = await this.getPrinterService();
            await printerService.inviteToGroup(groupId, this.user, targetUserId);

            await BaseSocket.emitStateChanged(printerService, this.server);
            this.socket.emit('user_invited', { success: true, userId: targetUserId });
        } catch (error: any) {
            this.socket.emit('user_invited', { success: false, error: error.message });
        }
    }

    async declineGroupInvite(groupId: string) {
        try {
            const printerService = await this.getPrinterService();
            await printerService.declineGroupInvite(groupId, this.user);

            await BaseSocket.emitStateChanged(printerService, this.server);
            this.socket.emit('invite_declined', { success: true, groupId });
        } catch (error: any) {
            this.socket.emit('invite_declined', { success: false, error: error.message });
        }
    }

    async generateGroupInvite() {
        const correlationId = logger.generateCorrelationId();
        try {
            const printerService = await this.getPrinterService();

            logger.socket('info', 'Client generating group invitation', {
                correlationId,
                userId: this.user.id,
                printerId: printerService.getPrinter().entity.id
            });
            const inviteToken = await printerService.generateGroupInvite(this.user);

            logger.socket('info', 'Group invitation generated successfully', {
                correlationId,
                userId: this.user.id,
                inviteToken: inviteToken.substring(0, 8) + '...'
            });

            this.socket.emit('group_invite_generated', { inviteToken });
            return inviteToken;
        } catch (error: any) {
            logger.socket('error', 'Failed to generate group invitation', {
                correlationId,
                userId: this.user.id,
                error: error.message
            });
            this.socket.emit('error', { message: 'Failed to generate group invitation' });
            // throw error;
        }
    }

    async joinGroupByInvite(inviteToken: string) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', 'Client attempting to join group by invitation', {
            correlationId,
            userId: this.user.id,
            inviteToken: inviteToken.substring(0, 8) + '...'
        });

        try {
            const printerService = await this.getPrinterService();
            const success = await printerService.joinGroupByInvite(inviteToken, this.user);

            if (success) {
                logger.socket('info', 'User joined group via invitation successfully', {
                    correlationId,
                    userId: this.user.id
                });
                this.socket.emit('group_joined_by_invite', { success: true });
            } else {
                logger.socket('warn', 'Invalid or expired group invitation', {
                    correlationId,
                    userId: this.user.id,
                    inviteToken: inviteToken.substring(0, 8) + '...'
                });
                this.socket.emit('group_joined_by_invite', { success: false, error: 'Invalid or expired invitation' });
            }

            await BaseSocket.emitStateChanged(printerService, this.server);
        } catch (error: any) {
            logger.socket('error', 'Failed to join group by invitation', {
                correlationId,
                userId: this.user.id,
                error: error.message
            });
            this.socket.emit('error', { message: 'Failed to join group' });
            // throw error;
        }
    }

    async changeContext(context: any) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('debug', 'User context change request', {
            correlationId,
            userId: this.user.id,
            hasContext: !!context,
            contextItemCount: context?.items?.length || 0,
            socketId: this.socket.id
        });

        try {
            const printerService = await this.getPrinterService();
            await printerService.userChangeContext(this.user, context);

            await BaseSocket.emitStateChanged(printerService, this.server);

            logger.socket('debug', 'User context updated successfully', {
                correlationId,
                userId: this.user.id,
                contextItemCount: context?.items?.length || 0,
                socketId: this.socket.id
            });
        } catch (error: any) {
            logger.socket('error', 'User context change failed', {
                correlationId,
                userId: this.user.id,
                error: error.message,
                socketId: this.socket.id
            });
        }
    }

    async makeOrder(args: CreateOrder) {
        const correlationId = logger.generateCorrelationId();

        logger.socket('info', 'Make order request via socket', {
            correlationId,
            userId: this.user.id,
            paymentMethod: args.paymentMethod,
            deliveryMethod: args.deliveryMethod,
            socketId: this.socket.id
        });

        try {
            const printerService = await this.getPrinterService();
            await printerService.makeOrder(this.user, args);

            await BaseSocket.emitStateChanged(printerService, this.server);

            logger.socket('info', 'Order created successfully via socket', {
                correlationId,
                userId: this.user.id,
                paymentMethod: args.paymentMethod,
                deliveryMethod: args.deliveryMethod,
                socketId: this.socket.id
            });
        } catch (error: any) {
            logger.socket('error', 'Socket order creation failed', {
                correlationId,
                userId: this.user.id,
                error: error.message,
                socketId: this.socket.id
            });
        }
    }
}

export { ClientSocket, ClientSocketEvent, ClientSocketOutputEvent };
