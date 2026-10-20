from aws_cdk import aws_events as events

a = events.EventBus(self, "A", event_bus_name="py-bus")
b = events.EventBus(self, "B")
c = events.EventBus.from_event_bus_name(self, "C", "py-existing")
