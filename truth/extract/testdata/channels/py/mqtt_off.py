class Bus:
    def go(self, client):
        client.subscribe('no/paho/import')
        client.publish('no/paho/import/out', 'x')
