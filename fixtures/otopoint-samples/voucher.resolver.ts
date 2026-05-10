import { Args, Context, Field, ID, Info, Mutation, ObjectType, Parent, Query, ResolveField, Resolver, ResolveReference } from "@nestjs/graphql";
import { PrismaService } from "../../prisma.service";
import { Merchant } from "../../@generated/merchant/merchant.model";
import { GraphQLResolveInfo } from "graphql";
import { getPrismaInclude } from "../helper/prisma-select";
import { PrismaSelect } from '@paljs/plugins';
import { MerchantWhereUniqueInput } from "../../@generated/merchant/merchant-where-unique.input";
import { MerchantWhereInput } from "../../@generated/merchant/merchant-where.input";
import { GqlAuth0AuthGuard, GqlAuthGuard, GqlHybridAuthGuard } from "../../auth/gql-auth.guard";
import { NotFoundException, UnauthorizedException, UseGuards } from "@nestjs/common";
import { RolesGuard } from "../../auth/roles.guard";
import { Roles } from "../../auth/roles.decorator";
import { UserPayload, UserRole } from "../../models/user.model";
import { User } from "../../@generated/user/user.model";
import { UserWhereInput } from "../../@generated/user/user-where.input";
import { BalanceService } from "../../services/balance.service";
import { Transaction } from "../../@generated/transaction/transaction.model";
import { UserCount } from "../../@generated/user/user-count.output";
import { VoucherTemplate } from "../../@generated/voucher-template/voucher-template.model";
import { VoucherTemplateWhereInput } from "../../@generated/voucher-template/voucher-template-where.input";
import { VoucherTemplateOrderByWithRelationInput } from "../../@generated/voucher-template/voucher-template-order-by-with-relation.input";
import { CreateOneVoucherTemplateArgs } from "../../@generated/voucher-template/create-one-voucher-template.args";
import { UpdateOneVoucherTemplateArgs } from "../../@generated/voucher-template/update-one-voucher-template.args";
import { DeleteOneVoucherTemplateArgs } from "../../@generated/voucher-template/delete-one-voucher-template.args";
import { VoucherTemplateScalarFieldEnum } from "../../@generated/voucher-template/voucher-template-scalar-field.enum";

import { getFile } from "../../services/file/temp-upload.service";
import { CloudinaryService } from "../../services/file/cloudinary.service";
import { randomUUID } from "node:crypto";
import { Prisma } from "@prisma/client";
@Resolver(() => VoucherTemplate)
export class VoucherResolver {
    constructor(
        private readonly prisma: PrismaService,
        private readonly cloudinary: CloudinaryService
    ) { }

    @UseGuards(GqlHybridAuthGuard)
    @Query(() => [VoucherTemplate])
    async findVoucherTemplates(
        @Args('where') where: VoucherTemplateWhereInput,
        @Info() info: GraphQLResolveInfo
    ) {
        const select = new PrismaSelect(info).value;
        return this.prisma.voucherTemplate.findMany({
            where,
            orderBy: { createdAt: 'desc' },
            ...select
        });
    }

    @UseGuards(GqlHybridAuthGuard)
    @Query(() => [VoucherTemplate])
    async simpleFindVoucherTemplates(
        @Args('where') where: VoucherTemplateWhereInput,
        @Info() info: GraphQLResolveInfo
    ) {
        const select = new PrismaSelect(info).value;
        return this.prisma.voucherTemplate.findMany({
            where,
            orderBy: { createdAt: 'desc' },
            ...select
        });
    }

    @UseGuards(GqlAuth0AuthGuard)
    @Mutation(() => VoucherTemplate)
    async createVoucherTemplate(@Args() args: CreateOneVoucherTemplateArgs) {
        const file_name = args.data.imageUrl;
        if (file_name) {
            const file = await getFile(file_name);
            const result = await this.cloudinary.uploadFile(file, 'vouchers');
            args.data.imageUrl = result.url;
        }
        return this.prisma.voucherTemplate.create(args as Prisma.VoucherTemplateCreateArgs);
    }

    @UseGuards(GqlAuth0AuthGuard)
    @Mutation(() => VoucherTemplate)
    async updateVoucherTemplate(@Args() args: UpdateOneVoucherTemplateArgs) {
        const file_name = args.data.imageUrl;
        if (file_name && file_name.set) {
            const file = await getFile(file_name.set);
            const result = await this.cloudinary.uploadFile(file, 'vouchers');
            args.data.imageUrl = { set: result.url };
        }
        return this.prisma.voucherTemplate.update(args as Prisma.VoucherTemplateUpdateArgs);
    }

    @UseGuards(GqlAuth0AuthGuard)
    @Mutation(() => VoucherTemplate)
    async deleteVoucherTemplate(@Args() args: DeleteOneVoucherTemplateArgs) {
        return this.prisma.voucherTemplate.delete(args as Prisma.VoucherTemplateDeleteArgs);
    }
}