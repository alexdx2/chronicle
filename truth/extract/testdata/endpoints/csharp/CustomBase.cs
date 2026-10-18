using Microsoft.AspNetCore.Mvc;

public class WidgetsController : BaseApiController
{
    [HttpGet]
    public IActionResult List() => Ok();

    [HttpGet("{id}")]
    public IActionResult Get(int id) => Ok();

    [HttpGet("/health")]
    public IActionResult Health() => Ok();
}

public class PlainController : Controller
{
    [HttpGet("{id}")]
    public IActionResult Get(int id) => Ok();
}
