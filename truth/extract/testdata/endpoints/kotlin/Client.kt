package demo

import io.ktor.client.request.get

suspend fun call(client: HttpClient) {
    get("/not-a-route") { }
}
