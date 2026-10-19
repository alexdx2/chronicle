package extract

import (
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func extractKeys(t *testing.T, files map[string]string, class string) []string {
	t.Helper()
	m := fstest.MapFS{}
	for p, c := range files {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	rep, err := Extract(m)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range rep.Facts {
		if string(f.Class) == class {
			out = append(out, f.Key)
		}
	}
	sort.Strings(out)
	return out
}

const pomSample = `<?xml version="1.0"?>
<project xmlns="http://maven.apache.org/POM/4.0.0"
         xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <parent>
    <groupId>org.springframework.boot</groupId>
    <artifactId>spring-boot-starter-parent</artifactId>
  </parent>
  <groupId>com.corp</groupId>
  <artifactId>stock-svc</artifactId>
  <dependencyManagement>
    <dependencies>
      <dependency><groupId>com.managed</groupId><artifactId>only-managed</artifactId></dependency>
    </dependencies>
  </dependencyManagement>
  <dependencies>
    <dependency>
      <groupId>org.springframework.kafka</groupId>
      <artifactId>spring-kafka</artifactId>
    </dependency>
    <!--
    <dependency><groupId>x</groupId><artifactId>commented</artifactId></dependency>
    -->
  </dependencies>
  <build><plugins><plugin>
    <groupId>org.springframework.boot</groupId><artifactId>spring-boot-maven-plugin</artifactId>
  </plugin></plugins></build>
</project>`

func TestMavenPomDepsAndService(t *testing.T) {
	files := map[string]string{"pom.xml": pomSample}
	if got := strings.Join(extractKeys(t, files, "deps"), ","); got != "maven:org.springframework.kafka:spring-kafka" {
		t.Fatalf("deps = %s", got)
	}
	if got := strings.Join(extractKeys(t, files, "services"), ","); got != "own:stockservice" && got != "own:stocksvc" {
		t.Fatalf("services = %s", got)
	}
}

func TestMavenPomWithoutSpringBootIsNoService(t *testing.T) {
	pom := strings.ReplaceAll(strings.ReplaceAll(pomSample, "spring-boot-maven-plugin", "maven-jar-plugin"), "spring-boot-starter-parent", "base-parent")
	if got := extractKeys(t, map[string]string{"pom.xml": pom}, "services"); len(got) != 0 {
		t.Fatalf("library pom gave a service: %v", got)
	}
}

func TestSpringConfigURLs(t *testing.T) {
	yml := "services:\n  catalog:\n    url: http://catalog-api:3000\n  local:\n    url: http://localhost:8080\n  docs:\n    url: https://example.com/swagger\n# other:\n#   url: http://commented-svc:1\n"
	got := extractKeys(t, map[string]string{"src/main/resources/application.yml": yml}, "http")
	if len(got) != 1 || got[0] != "catalog-api" {
		t.Fatalf("http = %v", got)
	}
	props := "billing.base-url=http://billing-api:8080\n#old.url=http://dead:1\n"
	got = extractKeys(t, map[string]string{"src/main/resources/application.properties": props}, "http")
	if len(got) != 1 || got[0] != "billing-api" {
		t.Fatalf("properties http = %v", got)
	}
}
