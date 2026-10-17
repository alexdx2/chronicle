[Route("api/a")]
public class AController : ControllerBase
{
    [HttpGet,
     Route("split")]
    public IActionResult Split() => Ok();

    [Microsoft.AspNetCore.Mvc.HttpPost("qualified")]
    public IActionResult Q() => Ok();
}
