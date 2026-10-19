package com.shop.client;

import org.springframework.web.bind.annotation.*;

@FeignClient(name = "catalog", url = "http://catalog:3000")
@RequestMapping("/feign")
public interface CatalogApi {

    @GetMapping("/products/{sku}")
    String product(@PathVariable String sku);
}
