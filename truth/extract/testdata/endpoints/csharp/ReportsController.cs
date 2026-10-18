using Microsoft.AspNetCore.Mvc;

[ApiController]
[Route("api/[controller]/[action]")]
public class ReportsController : ControllerBase
{
    [HttpGet]
    public IActionResult Daily() => Ok();

    [HttpGet(template: "named")]
    public IActionResult Named() => Ok();

    [HttpGet(Consts.Path)]
    public IActionResult Dynamic() => Ok();

    [Obsolete(Consts.HttpGet)]
    public IActionResult NotAVerb() => Ok();

    [Obsolete(Reason, Consts.HttpGet)]
    public IActionResult AlsoNotAVerb() => Ok();

    [Microsoft.AspNetCore.Mvc.HttpPost("qualified")]
    public IActionResult Qualified() => Ok();

    [HttpDelete("{id}", Name = "del")]
    public IActionResult Del(int id) => Ok();
}
