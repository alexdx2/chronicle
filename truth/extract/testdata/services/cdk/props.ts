import { Stack } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';
import * as cloudfront from 'aws-cdk-lib/aws-cloudfront';

export class P extends Stack {
  constructor(scope: any, id: string) {
    super(scope, id);
    new lambda.Function(this, 'LongFn', {
      runtime: lambda.Runtime.NODEJS_18_X,
      handler: 'index.handler',
      code: lambda.Code.fromAsset('a'),
      memorySize: 256,
      timeout: Duration.seconds(10),
      environment: { A: 'b' },
      tracing: lambda.Tracing.ACTIVE,
      description: 'long',
      functionName: 'long-name-fn',
    });
    new lambda.Function(this, 'RtFn', {
      runtime: new lambda.Runtime('nodejs20.x', lambda.RuntimeFamily.NODEJS),
      functionName: 'rt-name-fn',
    });
    new cloudfront.Function(this, 'Redirect', {
      code: cloudfront.FunctionCode.fromInline('x'),
    });
  }
}
