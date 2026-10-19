package other

type Record struct{ Topic string }

func f() { _ = Record{Topic: "not-kafka"} }
