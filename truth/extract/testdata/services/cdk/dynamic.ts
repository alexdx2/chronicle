import { Stack } from 'aws-cdk-lib';
import * as lambda from 'aws-cdk-lib/aws-lambda';

export class D extends Stack {
  constructor(scope: any, id: string, stage: string) {
    super(scope, id);
    new lambda.Function(this, 'Concat', {
      functionName: 'fn-' + stage,
    });
    new lambda.Function(this, 'Lowered', {
      functionName: 'Up'.toLowerCase(),
    });
    for (let i = 0; i < 3; i++) {
      new lambda.Function(this, 'C' + i, {
        runtime: lambda.Runtime.NODEJS_18_X,
      });
    }
  }
}
