import redis

r = redis.Redis()


def run():
    r.xadd('py-stream-a', {'k': 'v'})
    r.xread({'py-stream-b': '$', 'py-stream-c': '$'}, count=1)
    r.xreadgroup('g', 'me', {'py-stream-d': '>'}, count=1)
    r.xreadgroup(groupname='g', consumername='me', streams={'py-stream-e': '>'})
    r.publish('py-chan-a', 'x')
    p = r.pubsub()
    p.subscribe('py-chan-b', 'py-chan-c')
    p.psubscribe('py-pat-*')
    # r.publish('py-chan-commented', 'x')
    r.publish(chan_var, 'x')
    r.xadd(f'stream-{n}', {})
    print("r.publish('py-chan-printed', 1)")
