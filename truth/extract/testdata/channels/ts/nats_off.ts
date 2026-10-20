import { Subject } from 'rxjs';
const s = new Subject();
s.subscribe('looks.like.nats');
bus.publish('looks.like.nats.too', 1);
