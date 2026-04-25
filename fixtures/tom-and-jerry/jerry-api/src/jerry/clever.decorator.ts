import { SetMetadata, applyDecorators, UseGuards } from '@nestjs/common';

export const CLEVERNESS_KEY = 'cleverness_level';

/**
 * Custom decorator that marks an endpoint as requiring a certain cleverness level.
 * Jerry is always clever, but some actions require extra cleverness.
 */
export function RequiresClever(level: 'basic' | 'genius' | 'legendary') {
  return applyDecorators(
    SetMetadata(CLEVERNESS_KEY, level),
  );
}

// Used on jerry's escape routes:
// @RequiresClever('genius')
// @Post('escape')
// escape() { ... }
