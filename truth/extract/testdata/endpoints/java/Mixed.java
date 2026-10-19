package com.shop.web;

@RestController
public class HealthController {
    @GetMapping("/health")
    public String health() { return "ok"; }
}

@Controller
@RequestMapping({"/v1", "/v2"})
class PingController {
    @GetMapping("/ping")
    String ping() { return "pong"; }
}

class PlainService {
    @GetMapping("/not-a-route")
    void x() {}
}

@RestController
@RequestMapping(PREFIX)
class ConstPrefixController {
    @GetMapping("/skipped")
    void y() {}
}
