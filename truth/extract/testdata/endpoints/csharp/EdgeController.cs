using Microsoft.AspNetCore.Mvc;

[ApiController]
[Route("api/[controller]")]
public class EdgeController : ControllerBase
{
    [HttpPost("[action]")]
    [ProducesResponseType(
        typeof(Order), 201)]
    public IActionResult Create() => Ok();

    [HttpGet("{id}")]
    [ProducesResponseType(typeof(Order[]), 200)]
    public IActionResult Get(int id) => Ok();

    [SwaggerOperation(Summary = "Use HttpGet here")]
    [HttpPut("one")]
    public IActionResult One() => Ok();

    public class Req
    {
        public int Id { get; set; }
    }

    [HttpGet("after-nested")]
    public IActionResult AfterNested() => Ok();
}
