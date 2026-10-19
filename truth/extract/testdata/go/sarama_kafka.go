package messaging

import "github.com/IBM/sarama"

const auditTopic = "audit-events"

func send(p sarama.SyncProducer) {
	p.SendMessage(&sarama.ProducerMessage{Topic: "invoices", Value: nil})
	p.SendMessage(&sarama.ProducerMessage{
		Topic: auditTopic,
	})
	// p.SendMessage(&sarama.ProducerMessage{Topic: "commented"})
}

func recv(c sarama.Consumer, g sarama.ConsumerGroup, ctx context.Context) {
	c.ConsumePartition("partitioned", 0, sarama.OffsetNewest)
	g.Consume(ctx, []string{"grp-a", auditTopic}, handler)
}
