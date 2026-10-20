package shop;

import org.springframework.amqp.core.*;
import org.springframework.amqp.rabbit.annotation.*;
import org.springframework.amqp.rabbit.core.RabbitTemplate;

public class Mail {
    private final RabbitTemplate rabbitTemplate;
    private final MailSender mailSender;

    @RabbitListener(queues = "mail.in")
    public void onMail(String m) {}

    @RabbitListener(queues = {"multi.a", "multi.b"})
    public void onMulti(String m) {}

    @RabbitListener(bindings = @QueueBinding(
        value = @Queue(value = "bound.q", durable = "true"),
        exchange = @Exchange(value = "bound.x", type = "topic"),
        key = "k"))
    public void onBound(String m) {}

    @RabbitListener(queues = Names.QUEUE)
    public void onConst(String m) {}

    // @RabbitListener(queues = "commented")
    public void send() {
        rabbitTemplate.convertAndSend("out.exchange", "out.key", "msg");
        rabbitTemplate.convertAndSend("pp.key", "msg", m -> m);
        rabbitTemplate.convertAndSend("two-args", "msg");
        mailSender.send("k-topic", "key", "v");
        rabbitTemplate.convertAndSend(EXCHANGE, "k", "msg");
    }

    @Bean
    Queue mailQueue() { return new Queue("declared.q", true); }

    @Bean
    Queue built() { return QueueBuilder.durable("built.q").build(); }

    @Bean
    TopicExchange mailExchange() { return new TopicExchange("declared.x"); }
}
