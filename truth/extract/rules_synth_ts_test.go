package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestNpmPrivateNotPublished(t *testing.T) {
	cases := []struct{ name, pkg, want string }{
		{"private", `{"name": "admin-spa", "private": true}`, ""},
		{"public", `{"name": "@corp/logger", "version": "1.0.0"}`, "npm:@corp/logger"},
		{"private false", `{"name": "lib", "private": false}`, "npm:lib"},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{"package.json": c.pkg}, truth.ClassPackages); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestViteEnvAlias(t *testing.T) {
	client := "export const API_URL: string = import.meta.env.VITE_API_URL;\n"
	use := "import { API_URL } from './client';\nexport const f = () => fetch(`${API_URL}/api/items`);\n"
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"resolved by env file", map[string]string{".env.production": "VITE_API_URL=http://gateway:8080\n", ".env.development": "VITE_API_URL=http://localhost:8080\n", "src/client.ts": client, "src/use.ts": use}, "gateway"},
		{"local only keeps var", map[string]string{".env.development": "VITE_API_URL=http://localhost:8080\n", "src/client.ts": client, "src/use.ts": use}, "var:API_URL"},
		{"no alias keeps var", map[string]string{".env.production": "VITE_API_URL=http://gateway:8080\n", "src/use.ts": use}, "gateway,var:API_URL"},
		{"other project env", map[string]string{"other/.env": "VITE_API_URL=http://gateway:8080\n", "src/client.ts": client, "src/use.ts": use}, "gateway,var:API_URL"},
		{"commented alias", map[string]string{".env": "VITE_API_URL=http://gateway:8080\n", "src/client.ts": "// export const API_URL = import.meta.env.VITE_API_URL;\n", "src/use.ts": use}, "gateway,var:API_URL"},
	}
	for _, c := range cases {
		if got := keysOf(t, c.files, truth.ClassHTTP); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestExpoConfigEnvDefault(t *testing.T) {
	cfg := "module.exports = () => ({\n  extra: {\n    apiUrl: process.env.API_URL || 'http://gateway:8080',\n  },\n});\n"
	cases := []struct{ name, path, src, want string }{
		{"js", "app.config.js", cfg, "gateway"},
		{"ts", "app.config.ts", cfg, "gateway"},
		{"nullish", "app.config.ts", "export default { extra: { apiUrl: process.env.API_URL ?? \"https://api.example.com\" } };", "api.example.com"},
		{"localhost", "app.config.js", "module.exports = { extra: { apiUrl: process.env.API_URL || 'http://localhost:3000' } };", ""},
		{"commented", "app.config.js", "module.exports = {\n  // apiUrl: process.env.API_URL || 'http://gateway:8080',\n};", ""},
		{"not a config file", "src/util.ts", cfg, ""},
		{"other config file", "src/config.ts", cfg, ""},
		{"no fallback", "app.config.js", "module.exports = { extra: { apiUrl: process.env.API_URL } };", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassHTTP); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestNestGraphQLResolvers(t *testing.T) {
	src := `import { Args, Mutation, Query, Resolver } from '@nestjs/graphql';

@Resolver(() => Product)
export class ProductsResolver {
  @Query(() => [Product], { name: 'products' })
  list() {}

  @Query('legacy')
  old() {}

  @Query(() => Product)
  @UseGuards(AuthGuard('jwt'))
  async product(@Args('id') id: string) {}

  @Mutation(() => Product)
  renameProduct(
    @Args('id') id: string,
  ) {}

  @Mutation(returns => Boolean, {
    name: "dropProduct",
  })
  remove() {}

  @ResolveField()
  price() {}
}
`
	if got, want := keysOf(t, map[string]string{"src/p.resolver.ts": src}, truth.ClassEndpoints),
		"MUTATION dropProduct,MUTATION renameProduct,QUERY legacy,QUERY product,QUERY products"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	noise := map[string]string{
		"a.ts":      "export class A {\n  @Query() list() {}\n}\n",                              // no @Resolver
		"b.ts":      "@Resolver()\nexport class B {}\nexport class C {\n  @Query() x() {}\n}\n", // other class
		"c.ts":      "@Resolver()\nexport class D {\n  // @Query() gone() {}\n  log() { return '@Query() fake()'; }\n}\n",
		"d.spec.ts": "@Resolver()\nexport class E {\n  @Query() inTest() {}\n}\n",
		"e.ts":      "@Resolver()\nexport class F {\n  @Query(() => X, { name: `a${b}` })\n  tpl() {}\n}\n",
	}
	if got := keysOf(t, noise, truth.ClassEndpoints); got != "" {
		t.Errorf("noise: got %q", got)
	}
}
