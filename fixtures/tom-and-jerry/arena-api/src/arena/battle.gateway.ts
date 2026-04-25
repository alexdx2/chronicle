import {
  WebSocketGateway,
  WebSocketServer,
  SubscribeMessage,
  OnGatewayConnection,
  OnGatewayDisconnect,
} from '@nestjs/websockets';
import { Server, Socket } from 'socket.io';
import { ArenaService } from './arena.service';

@WebSocketGateway({ cors: true })
export class BattleGateway implements OnGatewayConnection, OnGatewayDisconnect {
  @WebSocketServer()
  server: Server;

  constructor(private readonly arenaService: ArenaService) {}

  handleConnection(client: Socket) {
    console.log(`Spectator connected: ${client.id}`);
  }

  handleDisconnect(client: Socket) {
    console.log(`Spectator disconnected: ${client.id}`);
  }

  @SubscribeMessage('watch-battle')
  handleWatchBattle(client: Socket) {
    return this.arenaService.getHistory();
  }

  broadcastBattleResult(event: any) {
    this.server.emit('battle-result', event);
  }
}
