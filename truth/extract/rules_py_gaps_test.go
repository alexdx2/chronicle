package extract

import (
	"sort"
	"strings"
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestPyEnvDefaultHTTP(t *testing.T) {
	cases := []struct{ name, path, src, want string }{
		{"os.getenv", "app/config.py", "import os\nES = os.getenv(\"ES_URL\", \"http://elasticsearch:9200\")\n", "elasticsearch"},
		{"environ.get", "proj/settings.py", "CAT = os.environ.get('CATALOG_API_URL', 'http://catalog-api:3000')\n", "catalog-api"},
		{"bare getenv", "app/config.py", "from os import getenv\nX = getenv('PRICING', 'https://pricing.internal.example/v1')\n", "pricing.internal.example"},
		{"airflow variable", "dags/cfg.py", "U = Variable.get(\"catalog_api_url\", default_var=\"http://catalog-api:3000\")\n", "catalog-api"},
		{"env default kw", "app/settings.py", "U = env('BILLING_URL', default='http://billing-api:8080')\n", "billing-api"},
		{"multi-line call", "app/config.py", "U = os.getenv(\n    \"ES_URL\",\n    \"http://elasticsearch:9200\",\n)\n", "elasticsearch"},
		{"local host", "app/config.py", "U = os.getenv('X_URL', 'http://localhost:9200')\nV = os.getenv('Y_URL', 'http://127.0.0.1:1')\n", ""},
		{"not a url", "app/config.py", "A = os.getenv('K', 'kafka:9092')\nB = os.getenv('D', 'postgresql://u@db:5432/x')\nC = os.getenv('N', '200')\n", ""},
		{"no default", "app/config.py", "A = os.getenv('X_URL')\nB = os.environ.get('Y_URL', DEFAULT)\n", ""},
		{"comment", "app/config.py", "# U = os.getenv('X_URL', 'http://old-api:80')\n", ""},
		{"docstring", "app/config.py", "\"\"\"\nU = os.getenv('X_URL', 'http://old-api:80')\n\"\"\"\n", ""},
		{"method named config", "app/x.py", "app.config('X', 'http://other:80')\n", ""},
		{"doc key", "app/config.py", "A = os.getenv('DOCS_URL', 'https://docs.example.com')\n", ""},
		{"test file", "tests/test_cfg.py", "A = os.getenv('X_URL', 'http://t-api:80')\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestRootDockerfileService(t *testing.T) {
	uv := "FROM python:3.12\nCMD [\"uvicorn\", \"indexer.main:app\", \"--port\", \"80\"]\n"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"fastapi title slug", map[string]string{
			"Dockerfile": uv, "indexer/main.py": "from fastapi import FastAPI\napp = FastAPI(title=\"search-indexer\")\n"}, "own:searchindexer"},
		{"title is prose", map[string]string{
			"Dockerfile": uv, "indexer/main.py": "app = FastAPI(title=\"Search Indexer API\")\n"}, ""},
		{"title commented", map[string]string{
			"Dockerfile": uv, "indexer/main.py": "# app = FastAPI(title=\"search-indexer\")\napp = FastAPI()\n"}, ""},
		{"poetry name", map[string]string{
			"Dockerfile": uv, "pyproject.toml": "[tool.poetry]\nname = \"ml-scoring\"\npackage-mode = false\n"}, "own:mlscoring"},
		{"project name", map[string]string{
			"Dockerfile": "FROM python\nENTRYPOINT [\"python\", \"-m\", \"worker\"]\n", "pyproject.toml": "[project]\nname = \"lead_worker\"\n"}, "own:leadworker"},
		{"setup.py name", map[string]string{
			"Dockerfile": "FROM python\nCMD python run.py\n", "setup.py": "setup(\n    name='billing-sync',\n)\n"}, "own:billingsync"},
		{"celery shell form", map[string]string{
			"Dockerfile": "FROM python\nCMD celery -A app.tasks worker -l info\n", "pyproject.toml": "[project]\nname = \"jobs\"\n"}, "own:jobs"},
		{"no name anywhere", map[string]string{"Dockerfile": uv}, ""},
		{"pip is not a service", map[string]string{
			"Dockerfile": "FROM python\nCMD [\"python\", \"-m\", \"pip\", \"list\"]\n", "pyproject.toml": "[project]\nname = \"x\"\n"}, ""},
		{"RUN is not an entry point", map[string]string{
			"Dockerfile": "FROM python\nRUN python -m compileall .\nRUN uvicorn --version\n", "pyproject.toml": "[project]\nname = \"x\"\n"}, ""},
		{"commented CMD", map[string]string{
			"Dockerfile": "FROM python\n# CMD [\"uvicorn\", \"a:app\"]\n", "pyproject.toml": "[project]\nname = \"x\"\n"}, ""},
		{"node dockerfile", map[string]string{
			"Dockerfile": "FROM node\nCMD [\"node\", \"server.js\"]\n", "pyproject.toml": "[project]\nname = \"x\"\n"}, ""},
	}
	for _, c := range cases {
		got := keysOf(t, c.files, truth.ClassServices)
		if got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestKafkaPySendMultiLine(t *testing.T) {
	head := "from kafka import KafkaProducer\nproducer = KafkaProducer(bootstrap_servers=[\"k:9092\"])\n"
	cases := []struct{ name, src, want string }{
		{"literal on next line", head + "producer.send(\n    \"score-computed\",\n    value={\"id\": 1},\n)\n", "topic:score-computed#produce"},
		{"topic keyword on next line", head + "producer.send(\n    topic='lead-created',\n    value=b'x',\n)\n", "topic:lead-created#produce"},
		{"same line still works", head + "producer.send('a-b', b'x')\n", "topic:a-b#produce"},
		{"variable topic", head + "producer.send(\n    TOPIC,\n    value=b'x',\n)\n", ""},
		{"concatenated topic", head + "producer.send(\n    \"x-\" + env,\n    value=b'x',\n)\n", ""},
		{"commented call", head + "# producer.send(\n#     \"old-topic\",\n# )\n", ""},
		{"log message naming topic", head + "log.info(\n    \"sent to score-computed\",\n)\nsock.send(\n    \"score-computed\",\n)\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"app/events.py": c.src}, truth.ClassChannels); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestDRFRouter(t *testing.T) {
	views := "from rest_framework import viewsets, mixins\n" +
		"class FullViewSet(viewsets.ModelViewSet):\n    queryset = X\n" +
		"class RoViewSet(viewsets.ReadOnlyModelViewSet):\n    queryset = X\n" +
		"class PlainViewSet(viewsets.ViewSet):\n    def list(self, request):\n        pass\n    def destroy(self, request, pk=None):\n        pass\n" +
		"class MixViewSet(mixins.ListModelMixin, mixins.CreateModelMixin, viewsets.GenericViewSet):\n    pass\n"
	routerURLs := "from rest_framework.routers import DefaultRouter\nfrom .views import FullViewSet, RoViewSet\n" +
		"router = DefaultRouter()\nrouter.register(r\"full\", FullViewSet, basename=\"f\")\nrouter.register(r'ro', RoViewSet)\nurlpatterns = router.urls\n"
	full := "GET /api/full,POST /api/full,GET /api/full/{},PUT /api/full/{},PATCH /api/full/{},DELETE /api/full/{}"
	ro := "GET /api/ro,GET /api/ro/{}"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"include module from project urls", map[string]string{
			"app/views.py": views, "app/urls.py": routerURLs,
			"proj/urls.py": "from django.urls import include, path\nurlpatterns = [path(\"api/\", include(\"app.urls\"))]\n",
		}, ro + "," + full},
		{"include router.urls in same file", map[string]string{
			"app/views.py": views,
			"app/urls.py": "from django.urls import include, path\nfrom .views import RoViewSet\nrouter = routers.SimpleRouter()\n" +
				"router.register('ro', RoViewSet)\nurlpatterns = [\n    path(\n        'api/',\n        include(router.urls),\n    ),\n]\n",
		}, ro},
		{"nested mount", map[string]string{
			"app/views.py": views, "app/urls.py": routerURLs,
			"proj/api.py":  "",
			"proj/urls.py": "urlpatterns = [path('v1/', include('app.urls'))]\n",
			"urls.py":      "urlpatterns = [path('svc/', include('proj.urls'))]\n",
		}, "GET /svc/v1/full,POST /svc/v1/full,GET /svc/v1/full/{},PUT /svc/v1/full/{},PATCH /svc/v1/full/{},DELETE /svc/v1/full/{},GET /svc/v1/ro,GET /svc/v1/ro/{}"},
		{"plain viewset and mixins", map[string]string{
			"app/views.py": views,
			"app/urls.py": "router = DefaultRouter()\nrouter.register(r'plain', PlainViewSet, basename='p')\nrouter.register(r'mix', MixViewSet)\nfrom .views import PlainViewSet, MixViewSet\n" +
				"urlpatterns = [path('', include(router.urls))]\n",
		}, "DELETE /plain/{},GET /plain,GET /mix,POST /mix"},
		{"module qualified viewset and viewset in the same file", map[string]string{
			"app/views.py": views,
			"app/urls.py": "from . import views\nrouter = DefaultRouter()\nrouter.register(r'full', views.RoViewSet)\nrouter.register(r'local', LocalVS)\n" +
				"class LocalVS(viewsets.ReadOnlyModelViewSet):\n    pass\nurlpatterns = [path('x/', include(router.urls))]\n",
		}, "GET /x/full,GET /x/full/{},GET /x/local,GET /x/local/{}"},
		{"inherited viewset", map[string]string{
			"app/views.py": views + "class Child(RoViewSet):\n    def create(self, r):\n        pass\n",
			"app/urls.py":  "from .views import Child\nrouter = DefaultRouter()\nrouter.register('c', Child)\nurlpatterns = [path('', include(router.urls))]\n",
		}, "GET /c,POST /c,GET /c/{}"},
		{"action and http_method_names", map[string]string{
			"app/views.py": "class V(viewsets.ModelViewSet):\n    http_method_names = ['get', 'post']\n" +
				"    @action(detail=True, methods=['post'], url_path='close')\n    def close_it(self, request, pk=None):\n        pass\n" +
				"    @action(detail=False)\n    def stats(self, request):\n        pass\n",
			"app/urls.py": "from .views import V\nrouter = DefaultRouter()\nrouter.register('v', V)\nurlpatterns = [path('', include(router.urls))]\n",
		}, "GET /v,POST /v,GET /v/{},POST /v/{}/close,GET /v/stats"},
		{"unresolved viewset", map[string]string{
			"app/urls.py": "from external_lib import ThingViewSet\nrouter = DefaultRouter()\nrouter.register('t', ThingViewSet)\nurlpatterns = [path('', include(router.urls))]\n",
		}, ""},
		{"commented register", map[string]string{
			"app/views.py": views,
			"app/urls.py":  "from .views import RoViewSet\nrouter = DefaultRouter()\n# router.register('ro', RoViewSet)\nurlpatterns = router.urls\n",
		}, ""},
		{"not a drf router", map[string]string{
			"app/views.py": views,
			"app/admin.py": "from .views import RoViewSet\nadmin.site.register(RoViewSet)\nregistry = Registry()\nregistry.register('ro', RoViewSet)\n",
		}, ""},
		{"test file", map[string]string{
			"app/views.py":      views,
			"app/tests/urls.py": "from app.views import RoViewSet\nrouter = DefaultRouter()\nrouter.register('ro', RoViewSet)\n",
		}, ""},
	}
	for _, c := range cases {
		got := keysOf(t, c.files, truth.ClassEndpoints)
		want := splitSorted(c.want)
		if got != want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, want)
		}
	}
}

func splitSorted(s string) string {
	if s == "" {
		return ""
	}
	p := strings.Split(s, ",")
	sort.Strings(p)
	return strings.Join(p, ",")
}
