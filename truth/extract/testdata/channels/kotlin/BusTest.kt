package demo

import org.apache.kafka.clients.producer.ProducerRecord
import org.apache.kafka.clients.consumer.KafkaConsumer

class Bus(private val consumer: KafkaConsumer<String, String>) {
    fun send(p: String) {
        val r = ProducerRecord("payment-captured", p.id, p)
        val r2 = ProducerRecord<String, String>("typed-topic", "v")
        val r3 = ProducerRecord(
            "multi-line",
            "k", "v"
        )
        // ProducerRecord("commented", "v")
        val dyn = ProducerRecord(topicName, "v")
        val tpl = ProducerRecord("events-$env", "v")
        val cat = ProducerRecord("a-" + env, "v")
        log.info("ProducerRecord(\"logged-topic\", x) not published to invoice-issued")
    }

    fun run() {
        consumer.subscribe(listOf("orders-in", "refunds-in"))
        consumer.subscribe(setOf<String>("single-set"))
        consumer.subscribe(Collections.singletonList("java-style"))
        consumer.subscribe(listOf(topicVar))
        consumer.subscribe(listOf("pre-$env"))
        // consumer.subscribe(listOf("old-topic"))
    }
}
