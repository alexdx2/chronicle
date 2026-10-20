import { EventEmitter } from 'events';
const e = new EventEmitter();
e.publish('chan-no-import', 1);
e.subscribe('chan-no-import-2');
