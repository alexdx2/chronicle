package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestAxumRoutes(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"chained verbs, both placeholder styles, commented route", map[string]string{"src/main.rs": `use axum::{routing::get, Router};
fn app() -> Router {
    Router::new()
        .route("/geo/:id", get(handlers::get_geo).post(handlers::set_geo))
        .route("/items/{item_id}/parts", axum::routing::delete(del))
        // .route("/geo/debug", get(handlers::debug))
        .route("/closure", get(|| async { client.get("/not-a-route") }))
        .with_state(state)
}
`}, "DELETE /items/{}/parts,GET /closure,GET /geo/{},POST /geo/{}"},
		{"inline nest and let-bound nest", map[string]string{"src/main.rs": `fn main() {
    let users = Router::new().route("/", get(list)).route("/:id", get(show));
    let app = Router::new()
        .nest("/api", Router::new().route("/ping", get(ping)))
        .nest("/users", users)
        .route("/health", get(health));
}
`}, "GET /api/ping,GET /health,GET /users,GET /users/{}"},
		{"fn-built router nested in the same file, merge", map[string]string{"src/main.rs": `fn v1() -> Router {
    Router::new().route("/a", get(a))
}
fn other() -> Router { Router::new().route("/o", post(o)) }
fn main() {
    let app = Router::new().nest("/v1", v1()).merge(other());
}
`}, "GET /v1/a,POST /o"},
		{"router nested from a sibling file", map[string]string{
			"src/main.rs":   "fn main() { let app = Router::new().nest(\"/orders\", routes::orders()); }\n",
			"src/routes.rs": "pub fn orders() -> Router { Router::new().route(\"/:id\", get(show)) }\n",
		}, "GET /orders/{}"},
		{"test module and string noise", map[string]string{"src/main.rs": `fn main() {
    let msg = "Router::new().route(\"/ghost\", get(x))";
    let app = Router::new().route("/real", get(r));
}
#[cfg(test)]
mod tests {
    fn t() { let app = Router::new().route("/in-test", get(x)); }
}
`}, "GET /real"},
		{"tests dir ignored", map[string]string{"tests/api.rs": "fn t() { Router::new().route(\"/x\", get(h)); }\n"}, ""},
	}
	for _, c := range cases {
		if got := keysOf(t, c.files, truth.ClassEndpoints); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestRustKafka(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"producer and consumer", `use rdkafka::producer::FutureRecord;
let r = FutureRecord::to("orders").payload(&p).key("k");
consumer.subscribe(&["lead-created", "lead-updated"]).unwrap();
`, "topic:lead-created#consume,topic:lead-updated#consume,topic:orders#produce"},
		{"noise", `use rdkafka::consumer::Consumer;
// consumer.subscribe(&["old"]);
println!("FutureRecord::to(\"ghost\")");
consumer.subscribe(&[topic.as_str()]).unwrap();
`, ""},
		{"no rdkafka", `x.subscribe(&["chan"]);` + "\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"src/k.rs": c.src}, truth.ClassChannels); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestRustHTTP(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"env default, split over lines", "let u = env::var(\"CATALOG_API_URL\")\n    .unwrap_or_else(|_| \"http://catalog-api:3000\".to_string());\n", "catalog-api"},
		{"std path, unwrap_or, String::from", "let u = std::env::var(\"PRICE_URL\").unwrap_or(String::from(\"https://price.internal\"));\n", "price.internal"},
		{"reqwest literal", "use reqwest;\nlet r = reqwest::get(\"http://geo-svc:80/x\").await?;\nlet c = client.post(\"https://pay.internal/charge\");\n", "geo-svc,pay.internal"},
		{"localhost and own address", "let a = env::var(\"X_URL\").unwrap_or_else(|_| \"http://localhost:1\".into());\nlet b = env::var(\"APP_URL\").unwrap_or_else(|_| \"https://me.example.com\".into());\n", ""},
		{"literal without a client crate", "let r = c.get(\"http://nowhere/x\");\n", ""},
		{"comment and string", "use reqwest;\n// let r = reqwest::get(\"http://old\");\nlet s = \"env::var(\\\"A_URL\\\").unwrap_or(\\\"http://txt\\\")\";\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"src/c.rs": c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestCargoDeps(t *testing.T) {
	lock := `version = 3

[[package]]
name = "app"
version = "0.1.0"
dependencies = [
 "serde",
 "tokio 1.0.0",
]

[[package]]
name = "serde"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"

[[package]]
name = "tokio"
version = "1.0.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
dependencies = ["mio"]

[[package]]
name = "mio"
version = "0.8.0"
source = "registry+https://github.com/rust-lang/crates.io-index"
`
	manifest := `[package]
name = "app"
version = "0.1.0"

[dependencies]
serde = "1"
tokio = { version = "1", features = ["full"] }
util = { path = "../util" }

[dev-dependencies]
# mio = "0.8"
`
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"manifest decides, transitive and own excluded", map[string]string{"Cargo.lock": lock, "Cargo.toml": manifest}, "cargo:serde,cargo:tokio"},
		{"no manifest: source-less lock package decides", map[string]string{"Cargo.lock": lock}, "cargo:serde,cargo:tokio"},
		{"rename and target tables", map[string]string{"Cargo.lock": lock, "Cargo.toml": "[package]\nname = \"app\"\n[dependencies]\nhttp_client = { package = \"tokio\", version = \"1\" }\n[target.'cfg(unix)'.dependencies]\nserde = \"1\"\n"}, "cargo:serde,cargo:tokio"},
		{"workspace member manifests", map[string]string{"Cargo.lock": lock, "Cargo.toml": "[workspace]\nmembers = [\"crates/a\"]\n", "crates/a/Cargo.toml": "[package]\nname=\"a\"\n[dependencies]\nserde=\"1\"\n"}, "cargo:serde"},
	}
	for _, c := range cases {
		if got := keysOf(t, c.files, truth.ClassDeps); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestCargoService(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"binary crate", map[string]string{"Cargo.toml": "[package]\nname = \"geo-rust\"\n", "src/main.rs": "fn main() {}\n"}, "own:georust"},
		{"library crate is not a service", map[string]string{"Cargo.toml": "[package]\nname = \"shared-types\"\n", "src/lib.rs": "pub struct A;\n"}, ""},
		{"lib plus bin", map[string]string{"Cargo.toml": "[package]\nname = \"svc\"\n", "src/lib.rs": "", "src/main.rs": "fn main() {}\n"}, "own:svc"},
		{"virtual workspace", map[string]string{"Cargo.toml": "[workspace]\nmembers = [\"a\"]\n"}, ""},
		{"member crate", map[string]string{"crates/api/Cargo.toml": "[package]\nname = \"api\"\n", "crates/api/src/main.rs": "fn main() {}\n"}, "own:api"},
	}
	for _, c := range cases {
		if got := keysOf(t, c.files, truth.ClassServices); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}
