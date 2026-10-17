var app = WebApplication.CreateBuilder(args).Build();
app.MapGet("/ping", () => "pong");
var api = app.MapGroup("/api/v1");
api.MapGet("/items", () => Results.Ok());
api.MapPost("/items/{id}", (int id) => Results.Ok());
// app.MapDelete("/commented", () => 1);
app.MapGroup("/chained").MapGet("/x", () => 1);
app.Run();
