import nats


async def main():
    nc = await nats.connect("nats://x")
    js = nc.jetstream()
    await nc.publish("py.out", b"x")
    await js.publish("py.durable", b"x")
    sub = await nc.subscribe("py.in", cb=handler)
    await js.subscribe("py.js", durable="d")
    await nc.request("py.rpc", b"")
    await js.add_stream(name="PYSTREAM", subjects=["py.>"])
    # await nc.publish("py.commented", b"")
    await nc.publish(subject_var, b"x")
    print("nc.subscribe('py.printed')")
