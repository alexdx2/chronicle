package com.shop.messaging;

import org.springframework.kafka.annotation.KafkaListener;
import org.springframework.kafka.core.KafkaTemplate;

@Component
public class Bus {
    private final KafkaTemplate<String, String> kafkaTemplate;
    private final KafkaTemplate rawTemplate;
    private final RestTemplate http;

    public Bus(KafkaTemplate<String, String> kafkaTemplate, KafkaTemplate rawTemplate, RestTemplate http) {
        this.kafkaTemplate = kafkaTemplate;
        this.rawTemplate = rawTemplate;
        this.http = http;
    }

    @KafkaListener(topics = "orders-created", groupId = "shop")
    public void onOrder(String m) {}

    @KafkaListener(
            id = "multi",
            topics = {"payments", "refunds"})
    public void onMoney(String m) {}

    @org.springframework.kafka.annotation.KafkaListener(topics = "qualified")
    public void onQualified(String m) {}

    @KafkaListeners({
            @KafkaListener(topics = "nested-a"),
            @KafkaListener(topics = "nested-b")
    })
    public void onNested(String m) {}

    // @KafkaListener(topics = "commented-out")
    @KafkaListener(topicPattern = "audit-.*")
    public void onPattern(String m) {}

    @KafkaListener(topics = "${shop.topic}")
    public void onPlaceholder(String m) {}

    @KafkaListener(topics = Topics.ORDERS)
    public void onConstant(String m) {}

    @KafkaListener(topics = "a-" + SUFFIX)
    public void onConcat(String m) {}

    public void publish(String k, String v) {
        kafkaTemplate.send("stock-changed", k, v);
        this.rawTemplate.send("raw-out", v);
        kafkaTemplate
                .send("chained-out", k, v);
        kafkaTemplate.send(TOPIC, k, v);
        kafkaTemplate.send("a-" + SUFFIX, v);
        kafkaTemplate.sendDefault("default-topic", v);
        http.send("not-kafka", v);
        log.debug("mirror feed to topic stock-audit is disabled; kafkaTemplate.send(\"in-log\", v)");
        // kafkaTemplate.send("commented-send", v);
    }

    public void record() {
        producer.send(new ProducerRecord<>("record-out", "k", "v"));
        producer.send(new ProducerRecord<String, String>(TOPIC, "v"));
    }
}
