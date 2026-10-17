import { Stack } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import { NodejsFunction } from 'aws-cdk-lib/aws-lambda-nodejs';

export class S extends Stack {
  constructor(scope: any, id: string) {
    super(scope, id);
    new lambda.Function(this, 'ResizeFn', {
      functionName: 'image-resizer',
      runtime: lambda.Runtime.NODEJS_18_X,
    });
    new NodejsFunction(this, 'AuditFn', {
      entry: 'audit.ts',
    });
    new lambda.Function(this, 'DynFn', {
      functionName: `${id}-dyn`,
    });
  }
}
