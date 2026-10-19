package extract

import (
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
