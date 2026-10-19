package messaging

import "github.com/twmb/franz-go/pkg/kgo"

func testOnly() { kgo.ConsumeTopics("only-in-test") }
