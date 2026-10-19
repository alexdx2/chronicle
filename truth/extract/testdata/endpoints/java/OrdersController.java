package com.shop.web;

import org.springframework.web.bind.annotation.*;

@RestController
@RequestMapping("/api/orders")
public class OrdersController {

    public record Body(int n) {}

    @GetMapping
    public String list() { return ""; }

    @GetMapping("/{id}")
    public String one(@PathVariable String id) { return id; }

    @PostMapping(path = "/{id}/pay", produces = "application/json")
    public String pay(@PathVariable String id) { return id; }

    @PutMapping(value = {"/a", "/b"})
    public void put() {}

    @DeleteMapping(
            "/{id}"
    )
    public void del() {}

    @PatchMapping("/{id}/note")
    public void patch() {}

    @RequestMapping(value = "/search", method = RequestMethod.GET)
    public void search() {}

    @RequestMapping(path = "/both", method = {RequestMethod.POST, RequestMethod.PUT})
    public void both() {}

    @RequestMapping("/any")
    public void any() {}

    // @GetMapping("/commented")
    /* @PostMapping("/block-commented") */

    private static final String NOTE = "see @GetMapping(\"/in-string\") for details";

    @GetMapping(BASE + "/constant")
    public void constant() {}

    @GetMapping("${orders.export.path}")
    public void placeholder() {}

    @Deprecated
    @GetMapping("/legacy")
    public void legacy() {}

    @Component
    static class Helper {
        @GetMapping("/inside-nested")
        void nested() {}
    }
}
