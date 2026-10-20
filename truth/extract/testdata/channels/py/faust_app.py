import faust

app = faust.App('demo', broker='kafka://localhost')

inbound = app.topic('f-in', value_type=bytes)
outbound = app.topic('f-out')
unused = app.topic('f-unused')
# commented = app.topic('f-commented')
both = app.topic('f-both', 'f-both-2', partitions=4)
dynamic = app.topic(f"f-{env}")


@app.agent(inbound)
async def work(stream):
    log.info("sending to f-logged")
    await outbound.send(value=b"x")
    async for e in stream:
        await both.send(value=e)


@app.agent(app.topic('f-inline'))
async def inline(stream):
    pass


@app.agent(both)
async def again(stream):
    pass
