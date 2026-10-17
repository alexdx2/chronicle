var v1 = app.MapGroup("/api").MapGroup("/v1");
v1.MapGet("/x", () => 1);
