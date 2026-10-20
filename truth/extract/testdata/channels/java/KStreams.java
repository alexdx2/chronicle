package shop;

import org.apache.kafka.streams.StreamsBuilder;
import org.apache.kafka.streams.kstream.Consumed;
import org.apache.kafka.streams.kstream.Produced;

class Topo {
    void build(StreamsBuilder builder) {
        var a = builder.stream("ks-in");
        var b = builder.table("ks-table", Consumed.with(null, null));
        var c = builder.globalTable("ks-global");
        // builder.stream("ks-commented");
        log.info("moving to topic ks-logged");
        String s = "builder.stream(\"ks-string\")";
        builder.stream(PREFIX + "ks-concat");
        builder.stream(IN_TOPIC);
        a.to("ks-out", Produced.with(null, null));
        a.through(
            "ks-through");
        a.to(dynamicExtractor);
    }
}
