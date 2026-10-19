package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestProtoRPC(t *testing.T) {
	cases := []struct{ name, path, src, want string }{
		{"two rpcs, commented one, message with rpc-like field", "proto/billing.proto", `syntax = "proto3";
service BillingService {
  rpc GetInvoice (GetInvoiceRequest) returns (Invoice);
  rpc ListInvoices(ListInvoicesRequest) returns (stream ListInvoicesResponse);
  // rpc VoidInvoice (VoidInvoiceRequest) returns (Invoice);
  /* rpc Old (A) returns (B); */
}
message Rpc { string rpc = 1; }
`, "RPC BillingService/GetInvoice,RPC BillingService/ListInvoices"},
		{"several services, options block, one-liner", "a.proto", `service A {
  rpc One (X) returns (Y) {
    option (google.api.http) = { get: "/v1/one" };
  }
  rpc Two (X) returns (Y);
}
service B { rpc Three (X) returns (Y); }
`, "RPC A/One,RPC A/Two,RPC B/Three"},
		{"third party protos are skipped", "third_party/google/x.proto", "service S { rpc M (A) returns (B); }\n", ""},
		{"string that looks like a service", "b.proto", "option x = \"service Z { rpc Q (A) returns (B); }\";\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassEndpoints); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}
