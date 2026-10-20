package bus

func run(nc *Other) {
	nc.Publish("not.nats", nil)
	nc.Subscribe("not.nats.either", nil)
}
