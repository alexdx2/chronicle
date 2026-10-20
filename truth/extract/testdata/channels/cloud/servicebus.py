from azure.servicebus import ServiceBusClient

client = ServiceBusClient.from_connection_string(conn)
a = client.get_queue_receiver(queue_name="py-q-in")
b = client.get_queue_receiver("py-q-pos", max_wait_time=5)
c = client.get_subscription_receiver(topic_name="py-topic", subscription_name="py-sub")
d = client.get_subscription_receiver("py-topic2", "py-sub2")
e = client.get_topic_sender(topic_name="py-topic-out")
f = client.get_queue_sender(queue_name="py-q-out")
# g = client.get_queue_sender(queue_name="py-gone")
h = client.get_queue_sender(queue_name=settings.QUEUE)
