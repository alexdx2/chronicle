package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestLaravelRoutes(t *testing.T) {
	cases := []struct{ name, path, src, want string }{
		{"verbs, apiResource only, commented route", "routes/api.php", `<?php
use Illuminate\Support\Facades\Route;
Route::get('/health', fn () => response()->json(['status' => 'ok']));
Route::apiResource('pages', PageController::class)->only(['index', 'show']);
Route::post('/leads/import', [LeadController::class, 'import']);
// Route::get('/drafts', [PageController::class, 'drafts']);
# Route::get('/hash', 'x');
/* Route::get('/block', 'x'); */
`, "GET /health,GET /pages,GET /pages/{},POST /leads/import"},
		{"resource except, nested resource", "routes/web.php", `<?php
Route::resource('photos', PhotoController::class)->except(['create', 'edit', 'destroy', 'update']);
Route::apiResource('photos.comments', CommentController::class)->only('index');
`, "GET /photos,GET /photos/{},GET /photos/{}/comments,POST /photos"},
		{"prefix group needs the file to say so", "routes/api.php", `<?php
Route::prefix('v1')->group(function () {
    Route::get('/users', [UserController::class, 'index']);
    Route::middleware('auth')->group(function () {
        Route::post('/users/{id}', function () {
            return 1;
        });
    });
});
Route::get('/outside', 'X@y');
Route::group(['prefix' => 'api/v2', 'middleware' => 'auth'], function () {
    Route::delete('items/{item}', 'ItemController@destroy');
});
Route::get('/after', fn () => 1);
`, "DELETE /api/v2/items/{},GET /after,GET /outside,GET /v1/users,POST /v1/users/{}"},
		{"chained prefix, match, view, multi-line", "routes/web.php", `<?php
Route::prefix('admin')
    ->middleware('auth')
    ->group(function () {
        Route::view('/dash', 'dash');
    });
Route::match(['get', 'post'], '/hook', [HookController::class, 'handle']);
Route::get(
    '/long',
    [LongController::class, 'show']
)->name('long');
Route::console('x', 'y');
`, "GET /admin/dash,GET /hook,GET /long,POST /hook"},
		{"not a routes file", "app/Http/routes_helper.php", "<?php\nRoute::get('/x', 'y');\n", ""},
	}
	for _, c := range cases {
		got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassEndpoints)
		if got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestPHPKafka(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"rdkafka consumer and producer", `<?php
$consumer = new RdKafka\KafkaConsumer($conf);
$consumer->subscribe(['lead-created', 'lead-updated']);
$topic = $rk->newTopic('audit-log');
`, "topic:audit-log#produce,topic:lead-created#consume,topic:lead-updated#consume"},
		{"low-level consumer topic", `<?php
$rk = new RdKafka\Consumer();
$topic = $rk->newTopic('in');
$topic->consumeStart(0, RD_KAFKA_OFFSET_BEGINNING);
`, "topic:in#consume"},
		{"log text, variable, comment", `<?php
use RdKafka\Conf;
$this->info('call ->subscribe(["ghost"])');
$consumer->subscribe([$name]);
$consumer->subscribe(['p-' . $env]);
// $consumer->subscribe(['old']);
`, ""},
		{"no kafka", "<?php\n$redis->subscribe(['chan']);\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"app/Cmd.php": c.src}, truth.ClassChannels); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.want)
		}
	}
}

func TestPHPHTTP(t *testing.T) {
	cases := []struct{ name, path, src, want string }{
		{"env default with client", "app/Svc.php", "<?php\nuse GuzzleHttp\\Client;\n$c = new Client(['base_uri' => env('CMS_URL', 'http://cms-svc:8000')]);\n", "cms-svc"},
		{"config services array", "config/services.php", "<?php\nreturn [\n 'catalog' => ['url' => env('CATALOG_URL', 'http://catalog-api:3000')],\n 'billing' => ['base_uri' => 'https://billing.internal'],\n];\n", "billing.internal,catalog-api"},
		{"Http facade", "app/S.php", "<?php\n$r = Http::get('http://geo-svc/lookup');\n", "geo-svc"},
		{"localhost, own url, kafka brokers", "config/app.php", "<?php\nreturn ['url' => env('APP_URL', 'http://localhost'), 'asset_url' => 'https://cdn.example.com', 'brokers' => env('KAFKA_BROKERS', 'kafka:9092')];\n", ""},
		{"comment", "app/S.php", "<?php\n// Http::get('http://old-svc/x');\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestComposerLock(t *testing.T) {
	lock := `{
    "packages": [
        {"name": "a/one", "version": "1.0.0", "require": {"b/two": "^1"}},
        {"name": "b/two", "version": "2.0.0"}
    ],
    "packages-dev": [
        {"name": "c/three", "version": "3.0.0"}
    ]
}`
	if got := keysOf(t, map[string]string{"composer.lock": lock}, truth.ClassDeps); got != "composer:a/one,composer:b/two,composer:c/three" {
		t.Errorf("got %s", got)
	}
}
