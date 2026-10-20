import mqtt from 'mqtt';

const client = mqtt.connect('mqtt://broker');
client.on('connect', () => {
  client.subscribe('sensors/+/temp');
  client.subscribe(['list/a', 'list/b'], { qos: 1 });
  client.subscribe({ 'obj/a': { qos: 0 }, 'obj/b': { qos: 1 } });
  client.publish('sensors/cmd', 'on');
  // client.publish('sensors/commented', 'on');
  client.publish(topicVar, 'x');
  client.publish(`sensors/${id}`, 'x');
});
client.on('message', (topic) => console.log("client.publish('sensors/logged')"));
