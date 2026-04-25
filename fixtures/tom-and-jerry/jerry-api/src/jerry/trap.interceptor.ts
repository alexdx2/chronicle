import { Injectable, NestInterceptor, ExecutionContext, CallHandler } from '@nestjs/common';
import { Observable, tap } from 'rxjs';

@Injectable()
export class TrapInterceptor implements NestInterceptor {
  intercept(context: ExecutionContext, next: CallHandler): Observable<any> {
    console.log('Checking for active traps before handler...');
    return next.handle().pipe(
      tap(data => {
        if (data?.trapTriggered) {
          console.log('TRAP TRIGGERED! Tom is stunned!');
        }
      }),
    );
  }
}
