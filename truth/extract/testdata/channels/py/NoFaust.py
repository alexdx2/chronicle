t = bus.topic('nf-topic')
@bus.agent(t)
def f(): t.send(1)
