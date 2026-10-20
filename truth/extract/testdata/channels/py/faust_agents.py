from fraud.app import (
    shared as sh,
    plain,
)
import faust

app = faust.App('x')


@app.agent(sh)
async def a(stream):
    await plain.send(value=1)
