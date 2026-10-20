package bus

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func run(ctx context.Context, nc *nats.Conn, js nats.JetStreamContext, njs jetstream.JetStream) {
	nc.Publish("orders.created", nil)
	js.Publish("orders.durable", nil)
	js.PublishAsync("orders.async", nil)
	njs.Publish(ctx, "orders.ctx", nil)
	nc.Request("orders.rpc", nil, 0)
	nc.Subscribe("orders.in", func(m *nats.Msg) {})
	nc.QueueSubscribe("orders.work", "workers", func(m *nats.Msg) {})
	js.Subscribe("orders.js", func(m *nats.Msg) {})
	js.PullSubscribe("orders.pull", "dur")
	nc.PublishMsg(&nats.Msg{Subject: "orders.msg"})
	js.AddStream(&nats.StreamConfig{
		Name:     "ORDERS",
		Subjects: []string{"orders.>"},
	})
	njs.CreateStream(ctx, jetstream.StreamConfig{Name: "NEWAPI", Subjects: []string{"new.>"}})

	// nc.Publish("commented.out", nil)
	nc.Publish(subjectVar, nil)
	nc.Publish("prefix."+suffix, nil)
	log.Info("calling nc.Publish(\"in.a.log\", x) and nc.Subscribe(\"log.too\")")
	js.AddStream(&nats.StreamConfig{Name: nameVar})
}
