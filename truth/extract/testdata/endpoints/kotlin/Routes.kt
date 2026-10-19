package demo

import io.ktor.server.routing.*
import io.ktor.http.HttpMethod

const val DYN = "/dyn"

fun Application.module() {
    routing {
        get("/health") { call.respondText("ok") }
        // get("/commented") { }
        /* post("/blocked") { } */
        route("/api") {
            get("/items/{id}") { }
            route("/v2") {
                post("/items") { }
                put("items/{id?}") { }
            }
            authenticate("jwt") {
                delete("/items/{id}") { }
            }
            get { call.respond("list") }
            route("/export", HttpMethod.Post) { handle { } }
            route("/sync") {
                method(HttpMethod.Put) { handle { } }
            }
            get(
                "/multi/line"
            ) { }
            get(DYN) { }
            get("/t/$id") { }
            route(DYN) {
                get("/hidden") { }
            }
        }
        patch("/top") { }
        val s = "get(\"/instring\") { }"
        log.info("post(\"/logged\") { }")
        client.get("/client") { }
        http.post("/client2") { }
    }
}

class Fake {
    fun get(x: String) = x
}
