import faust

app = faust.App('x')
shared = app.topic('f-shared')
plain = app.topic('f-plain')
