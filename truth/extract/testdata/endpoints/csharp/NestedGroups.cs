var api = app.MapGroup("/api");
var orders = api.MapGroup("/orders");
orders.MapGet("/{id}", (int id) => 1);
RouteGroupBuilder items = app.MapGroup("/items");
items.MapGet("/", () => 1);
other.MapGet("/unknown", () => 1);
