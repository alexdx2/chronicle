import { Controller, Get, Post, Put, Delete } from '@nestjs/common';

@Controller('cats')
export class CatsController {
  @Get()
  findAll() {}

  @Get(':id')
  findOne() {}

  @Post('/new')
  create() {}

  // @Delete('gone')
  /* @Put('hidden') */
  @Delete(':id')
  remove() {}
}

@Controller({ path: 'dogs' })
export class DogsController {
  @Get('list') list() {}
}

@Controller(['a', 'b'])
export class MultiController {
  @Get('x') x() {}
}

@Controller()
export class RootController {
  @Get('health') h() {}
}
