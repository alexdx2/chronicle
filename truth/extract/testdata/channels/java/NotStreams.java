package shop;

class Other {
    void run(Seq seq) {
        seq.stream("looks-like-a-topic");
        seq.to("elsewhere");
    }
}
