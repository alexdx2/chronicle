import boto3
from kafka import KafkaConsumer, KafkaProducer

consumer = KafkaConsumer('py-orders', 'py-refunds', bootstrap_servers='x')
consumer.subscribe(['sub-a', "sub-b"])
consumer.subscribe('sub-c')
producer.send('py-out', b'x')
producer.send(topic_var, b'x')
# producer.send('py-commented', b'')
sqs = boto3.client('sqs')
sqs.get_queue_url(QueueName='py-queue')
sqs.send_message(QueueUrl='https://sqs.eu-central-1.amazonaws.com/1/py-send', MessageBody='x')
# ------
# ------
# ------
# ------
# ------
# ------
# ------
# ------
sqs.receive_message(QueueUrl='https://sqs.eu-central-1.amazonaws.com/1/py-recv')
