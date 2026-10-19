package extract

import (
	"testing"

	"github.com/alexdx2/chronicle-core/truth"
)

func TestGradleDeps(t *testing.T) {
	kts := `plugins { id("io.ktor.plugin") version "2.3.12" }
buildscript { dependencies { classpath("com.example:tooling:1.0") } }
dependencies {
    implementation("io.ktor:ktor-server-core:2.3.12")
    implementation(platform("org.springframework.boot:spring-boot-dependencies:3.2.0"))
    api("a.b:no-version")
    runtimeOnly("x.y:with-var:$ver")
    testImplementation("io.ktor:ktor-server-test-host:2.3.12")
    implementation(
        "multi.line:artifact:1.0"
    )
    implementation(group = "org.named", name = "kts-named", version = "1.0")
    implementation(project(":core"))
    implementation(libs.ktor.core)
    implementation("$group:dyn:1.0")
    // implementation("commented:out:1.0")
    val s = "implementation(\"in.string:x:1\")"
}
`
	groovy := `dependencies {
    implementation 'org.apache.kafka:kafka-clients:3.7.1'
    testImplementation "junit:junit:4.13"
    implementation group: 'org.groovy', name: 'named', version: '2.0'
    implementation 'org.a:b:1', 'org.c:d:2'
    compile files('libs/x.jar')
}
`
	want := "maven:a.b:no-version,maven:io.ktor:ktor-server-core,maven:io.ktor:ktor-server-test-host,maven:multi.line:artifact,maven:org.named:kts-named,maven:org.springframework.boot:spring-boot-dependencies,maven:x.y:with-var"
	if got := keysOf(t, map[string]string{"build.gradle.kts": kts}, truth.ClassDeps); got != want {
		t.Errorf("kts: got %q\nwant %q", got, want)
	}
	want = "maven:junit:junit,maven:org.a:b,maven:org.apache.kafka:kafka-clients,maven:org.c:d,maven:org.groovy:named"
	if got := keysOf(t, map[string]string{"svc/build.gradle": groovy}, truth.ClassDeps); got != want {
		t.Errorf("groovy: got %q\nwant %q", got, want)
	}
	if got := keysOf(t, map[string]string{"other.gradle.kts": kts}, truth.ClassDeps); got != "" {
		t.Errorf("non build file: got %q", got)
	}
}

func TestGradleRootProjectService(t *testing.T) {
	cases := []struct{ name, path, src, want string }{
		{"kts", "settings.gradle.kts", "rootProject.name = \"payments-kotlin\"\ninclude(\"core\")\n", "own:paymentskotlin"},
		{"groovy", "settings.gradle", "rootProject.name = 'billing'\n", "own:billing"},
		{"commented", "settings.gradle.kts", "// rootProject.name = \"old\"\n", ""},
		{"dynamic", "settings.gradle.kts", "rootProject.name = \"svc-$env\"\n", ""},
		{"no name", "settings.gradle.kts", "include(\"a\")\n", ""},
		{"other file", "build.gradle.kts", "rootProject.name = \"x\"\n", ""},
	}
	for _, c := range cases {
		if got := keysOf(t, map[string]string{c.path: c.src}, truth.ClassServices); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}
