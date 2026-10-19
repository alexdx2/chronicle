package messaging

import (
	"context"
	"log/slog"

	"github.com/twmb/franz-go/pkg/kgo"
)

func consume(brokers []string, dyn string) {
	kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumeTopics("orders-created", "orders-paid"),
	)
	kgo.NewClient(kgo.ConsumeTopics(
		"multi-line",
		TopicShipped,
		dyn,
		"prefix-"+dyn,
	))
	// kgo.ConsumeTopics("commented-out")
	slog.Info("kgo.ConsumeTopics(\"in-a-string\")")
	_ = kgo.ConsumeTopics(UnknownTopic)
}

func produce(ctx context.Context, cl *kgo.Client) {
	cl.Produce(ctx, &kgo.Record{Topic: "orders-audit", Value: nil}, nil)
	cl.Produce(ctx, &kgo.Record{
		Key:   []byte("k"),
		Topic: TopicShipped,
		Value: nil,
	}, nil)
	cl.Produce(ctx, &kgo.Record{Value: nil}, nil)
	cl.Produce(ctx, &kgo.Record{Topic: dyn}, nil)
	// old: &kgo.Record{Topic: "retired"}
	slog.Info("&kgo.Record{Topic: \"logged-only\"}")
	kgo.DefaultProduceTopic("fallback-topic")
}
