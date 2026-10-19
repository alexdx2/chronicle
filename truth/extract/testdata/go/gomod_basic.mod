// module comment: module github.com/ignored/commented
module github.com/corp/shipping-go

go 1.22

require (
	github.com/go-chi/chi/v5 v5.0.12
	github.com/twmb/franz-go v1.16.1 // kafka client
	// github.com/commented/out v1.0.0
	"github.com/quoted/pkg" v0.3.0
)

require (
	github.com/klauspost/compress v1.17.7 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.7.0 // indirect
)

require github.com/google/uuid v1.6.0

require golang.org/x/text v0.14.0 // indirect

replace github.com/google/uuid => ../uuid

exclude github.com/bad/mod v1.0.0
