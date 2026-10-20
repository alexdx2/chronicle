import logging
from google.cloud import pubsub_v1

publisher = pubsub_v1.PublisherClient()
subscriber = pubsub_v1.SubscriberClient()
T = publisher.topic_path("proj", "ps-topic")
# OLD = publisher.topic_path("proj", "ps-old")
FULL = "projects/proj/topics/ps-full"
S = subscriber.subscription_path("proj", "ps-sub")
SFULL: str = "projects/proj/subscriptions/ps-sub-full"
UNUSED = publisher.topic_path("proj", "ps-unused")
DYN = publisher.topic_path("proj", name)


def go():
    publisher.publish(T, b"x")
    publisher.publish(topic=FULL, data=b"y")
    publisher.publish(publisher.topic_path("proj", "ps-inline"), b"z")
    publisher.publish(f"projects/{proj}/topics/ps-fstring", b"z")
    publisher.publish(DYN, b"z")
    log.info("publish(topic ps-logged) done")
    redis.publish("not-pubsub", "m")
    subscriber.subscribe(S, callback=cb)
    subscriber.subscribe(subscription=SFULL, callback=cb)
    subscriber.pull(request={"subscription": S, "max_messages": 5})
    subscriber.pull(subscription=subscriber.subscription_path("proj", "ps-pull"), max_messages=1)
    subscriber.subscribe(T, callback=cb)
