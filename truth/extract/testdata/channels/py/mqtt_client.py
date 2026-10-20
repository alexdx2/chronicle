import paho.mqtt.client as mqtt
import paho.mqtt.publish as publish

client = mqtt.Client()


def on_connect(client, userdata, flags, rc):
    client.subscribe('devices/+/telemetry')
    client.subscribe("fleet/#", qos=1)
    client.subscribe([("multi/a", 0), ("multi/b", 1)])
    client.subscribe(topic="kw/topic")
    # client.subscribe('devices/commented')
    client.subscribe(topic_var)
    client.subscribe(f"devices/{dev}/x")


def send():
    client.publish('devices/gw1/commands', '{"cmd": "sleep"}', qos=1)
    client.publish(topic="kw/out", payload="x")
    publish.single("single/out", "x", hostname="h")
    client.publish(out_topic, "x")
    log.warning("sending to client.publish('devices/logged', x)")
