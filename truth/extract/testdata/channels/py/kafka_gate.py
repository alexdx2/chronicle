from kafka import KafkaProducer
sock.send('hello-socket')
redis.pubsub().subscribe('redis-chan')
p = KafkaProducer(bootstrap_servers='x')
p.send('assigned-out', b'x')
