package demo

fun f(rx: Observable<String>) {
    rx.subscribe(listOf("not-a-topic"))
}
