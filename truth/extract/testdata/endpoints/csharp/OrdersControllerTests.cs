using Microsoft.AspNetCore.Mvc;

namespace Shop.Api.Controllers
{
    [ApiController]
    [Route("api/[controller]")]
    public class OrdersControllerTests : ControllerBase
    {
        [HttpGet]
        public IActionResult List() => Ok();

        [HttpGet("{id}")]
        public IActionResult Get(int id) => Ok();

        [HttpPost]
        [Authorize]
        public IActionResult Create() => Ok();

        [HttpPut("{id}"), Authorize]
        public IActionResult Update(int id) => Ok();

        [HttpDelete("~/api/legacy/{id}")]
        public IActionResult Remove(int id) => Ok();

        [Route("export")]
        public IActionResult Export() => Ok();

        [Route("search")]
        [HttpPost]
        public IActionResult Search() => Ok();

        [HttpGet("[action]")]
        public IActionResult Summary() => Ok();

        [HttpGet("both")]
        [HttpPost("both")]
        public IActionResult Both() => Ok();

        /*
        [HttpGet("hidden")]
        public IActionResult Hidden() => Ok();
        */
        // [HttpGet("commented")]

        [NonAction]
        public void Helper() { }
    }

    public class HealthController : ControllerBase
    {
        [HttpGet("health")]
        public IActionResult Check() => Ok();

        [HttpGet]
        public IActionResult NoRoute() => Ok();
    }
}
