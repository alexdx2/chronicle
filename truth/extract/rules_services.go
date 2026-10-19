package extract

import (
	"bytes"
	"encoding/json"
	"path"
	"regexp"
	"strings"

	"github.com/alexdx2/chronicle-core/truth"
	"github.com/alexdx2/chronicle-core/truth/extract/lex"
	"gopkg.in/yaml.v3"
)

// Services and infrastructure: compose, Kubernetes, Helm, Terraform, CDK,
// .NET web projects, Nest bootstraps and Python Dockerfiles. The key uses
// CanonName because service names are compared tolerantly.

func init() {
	Rules = append(Rules,
		funcRule{"compose.service", isComposePath, applyComposeService},
		funcRule{"k8s.workload", func(p string) bool { return hasExt(p, ".yaml", ".yml") && !isComposePath(p) }, applyK8sWorkload},
		funcRule{"helm.chart", func(p string) bool { return baseName(p) == "Chart.yaml" }, applyHelmChart},
		funcRule{"terraform.compute", func(p string) bool { return hasExt(p, ".tf") }, applyTerraformCompute},
		funcRule{"cdk.function", func(p string) bool { return hasExt(p, ".ts", ".js", ".py") }, applyCDKFunction},
		funcRule{"csharp.web_sdk", func(p string) bool { return hasExt(p, ".csproj") }, applyCsproj},
		funcRule{"nest.bootstrap", func(p string) bool { return hasExt(p, ".ts") }, applyNestBootstrap},
		funcRule{"python.entry", isDockerfilePath, applyPythonEntry},
	)
}

// serviceFact builds a services fact; names that are not literal are refused.
func serviceFact(rule string, f *FileCtx, kind, name, tech, image string, primary lex.Line, derived ...truth.Evidence) (truth.Fact, bool) {
	name = strings.TrimSpace(name)
	if name == "" || strings.ContainsAny(name, "$`{}") || strings.Contains(name, "%(") {
		return truth.Fact{}, false
	}
	if truth.CanonName(name) == "" {
		return truth.Fact{}, false
	}
	return truth.Fact{
		Class:   truth.ClassServices,
		Key:     truth.ServiceKey(kind, truth.CanonName(name)),
		Attrs:   map[string]string{"name": name, "kind": kind, "tech": tech, "image": image},
		Primary: evidence(f, primary),
		Derived: derived,
		Sources: []string{rule},
	}, true
}

// ---- compose ----

var infraTechs = []string{"postgres", "mysql", "mariadb", "mongo", "redis", "rabbitmq", "kafka", "zookeeper", "elasticsearch", "opensearch", "localstack", "minio", "nats", "memcached"}

// imageRepo strips registry/path prefix and tag or digest from an image ref.
func imageRepo(image string) string {
	if i := strings.Index(image, "@"); i >= 0 {
		image = image[:i]
	}
	image = image[strings.LastIndex(image, "/")+1:]
	if i := strings.Index(image, ":"); i >= 0 {
		image = image[:i]
	}
	return strings.ToLower(image)
}

func infraTech(image string) string {
	repo := imageRepo(image)
	for _, t := range infraTechs {
		if strings.Contains(repo, t) {
			return t
		}
	}
	return ""
}

func yamlResolve(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// yamlMapMerged is yamlMap that also follows "<<" merge keys (aliases).
func yamlMapMerged(n *yaml.Node, key string) *yaml.Node {
	return yamlMapMergedDepth(n, key, 0)
}

func yamlMapMergedDepth(n *yaml.Node, key string, depth int) *yaml.Node {
	n = yamlResolve(n)
	if n == nil || n.Kind != yaml.MappingNode || depth > 8 {
		return nil
	}
	if v := yamlMap(n, key); v != nil {
		return v
	}
	m := yamlMap(n, "<<")
	if m == nil {
		return nil
	}
	m = yamlResolve(m)
	if m.Kind == yaml.SequenceNode {
		for _, it := range m.Content {
			if v := yamlMapMergedDepth(it, key, depth+1); v != nil {
				return v
			}
		}
		return nil
	}
	return yamlMapMergedDepth(m, key, depth+1)
}

func applyComposeService(f *FileCtx) []truth.Fact {
	var doc yaml.Node
	if err := yaml.Unmarshal(f.Raw, &doc); err != nil {
		return nil
	}
	services := yamlMap(yamlDoc(&doc), "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return nil
	}
	var out []truth.Fact
	for i := 0; i+1 < len(services.Content); i += 2 {
		key, svc := services.Content[i], yamlResolve(services.Content[i+1])
		if strings.HasPrefix(key.Value, "x-") { // compose extension field, not a service
			continue
		}
		if svc.Kind != yaml.MappingNode {
			continue
		}
		l, ok := yamlLine(f, key.Line)
		if !ok {
			continue
		}
		image := ""
		if n := yamlMapMerged(svc, "image"); n != nil && n.Kind == yaml.ScalarNode {
			image = n.Value
		}
		kind, tech := "external", ""
		switch {
		case yamlMapMerged(svc, "build") != nil:
			kind = "own"
		case image != "":
			if tech = infraTech(image); tech != "" {
				kind = "infra"
			}
		}
		if fc, ok := serviceFact("compose.service", f, kind, key.Value, tech, image, l); ok {
			out = append(out, fc)
		}
	}
	return out
}

// ---- kubernetes / helm ----

var k8sWorkloads = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true, "CronJob": true, "Job": true}

func applyK8sWorkload(f *FileCtx) []truth.Fact {
	if !bytes.Contains(f.Raw, []byte("kind:")) {
		return nil
	}
	var out []truth.Fact
	yamlDocs(f.Raw, func(root *yaml.Node) {
		if k := yamlMap(root, "kind"); k == nil || !k8sWorkloads[k.Value] {
			return
		}
		name := yamlMap(yamlMap(root, "metadata"), "name")
		if name == nil || name.Kind != yaml.ScalarNode {
			return
		}
		if l, ok := yamlLine(f, name.Line); ok {
			if fc, ok := serviceFact("k8s.workload", f, "own", name.Value, "", "", l); ok {
				out = append(out, fc)
			}
		}
	})
	return out
}

func applyHelmChart(f *FileCtx) []truth.Fact {
	var doc yaml.Node
	if err := yaml.Unmarshal(f.Raw, &doc); err != nil {
		return nil
	}
	root := yamlDoc(&doc)
	if t := yamlMap(root, "type"); t != nil && t.Value == "library" {
		return nil
	}
	name := yamlMap(root, "name")
	if name == nil || name.Kind != yaml.ScalarNode {
		return nil
	}
	if l, ok := yamlLine(f, name.Line); ok {
		if fc, ok := serviceFact("helm.chart", f, "own", name.Value, "", "", l); ok {
			return []truth.Fact{fc}
		}
	}
	return nil
}

// ---- terraform ----

var (
	tfCompute = regexp.MustCompile(`^\s*resource\s+"(aws_lambda_function|aws_ecs_service|aws_ecs_task_definition)"\s+"[^"]+"\s*\{`)
	tfNameKey = map[string]string{"aws_lambda_function": "function_name", "aws_ecs_service": "name", "aws_ecs_task_definition": "family"}
)

func applyTerraformCompute(f *FileCtx) []truth.Fact {
	var out []truth.Fact
	tfResourceAttrs(f, tfCompute, func(typ string) string { return tfNameKey[typ] },
		func(res, l lex.Line, _, val string) {
			if fc, ok := serviceFact("terraform.compute", f, "own", val, "", "", l, evidence(f, res)); ok {
				out = append(out, fc)
			}
		})
	return out
}

// ---- cdk ----

var (
	cdkFnTS = regexp.MustCompile(`new\s+(?:\w*lambda\w*\.)?(?:Function|NodejsFunction|DockerImageFunction|PythonFunction)\(\s*\w+\s*,\s*['"]([^'"]+)['"]\s*,`)
	cdkFnPy = regexp.MustCompile(`\b\w*lambda\w*\.(?:Function|DockerImageFunction|PythonFunction)\(\s*\w+\s*,\s*['"]([^'"]+)['"]\s*,`)
	// The name literal must be closed by a separator: 'fn-' + stage and
	// 'Up'.toLowerCase() are expressions, not literals.
	cdkNameTS  = regexp.MustCompile(`\bfunctionName\s*:\s*(['"])([^'"]*)['"]\s*[,}]`)
	cdkNamePy  = regexp.MustCompile(`\bfunction_name\s*=\s*(['"])([^'"]*)['"]\s*[,)]`)
	cdkNameAny = regexp.MustCompile(`\b(?:functionName\s*:|function_name\s*=)`)
)

func applyCDKFunction(f *FileCtx) []truth.Fact {
	if !codeContains(f, "aws-cdk-lib", "aws_cdk", "@aws-cdk/") {
		return nil
	}
	re, nameRe := cdkFnTS, cdkNameTS
	if hasExt(f.Path, ".py") {
		re, nameRe = cdkFnPy, cdkNamePy
	}
	var out []truth.Fact
	for i, l := range f.Lines {
		text := callText(f.Lines, i, 40)
		loc := re.FindStringSubmatchIndex(text)
		if loc == nil || loc[0] >= len(l.Code) {
			continue
		}
		id := text[loc[2]:loc[3]]
		rest := text[loc[1]:]
		name := id
		if cdkNameAny.MatchString(rest) {
			nm := nameRe.FindStringSubmatch(rest)
			if nm == nil {
				continue // functionName is set but not a plain literal
			}
			name = nm[2]
		}
		if fc, ok := serviceFact("cdk.function", f, "own", name, "", "", l); ok {
			out = append(out, fc)
		}
	}
	return out
}

// ---- csproj ----

var (
	csprojAssembly = regexp.MustCompile(`<AssemblyName>\s*([^<]*?)\s*</AssemblyName>`)
	csprojComment  = regexp.MustCompile(`<!--.*?-->`)
	// Old-style (non-SDK) ASP.NET Framework projects: web project type guids
	// (web application, MVC 1-5, web site) or a reference to System.Web*.
	csprojWebGuid = regexp.MustCompile(`(?i)<ProjectTypeGuids>[^<]*(?:349C5851-65DF-11DA-9384-00065B846F21|603C0E0B-DB56-11DC-BE95-000D561079B0|F85E285D-A4E0-4152-9332-AB1D724D3325|E53F8FEA-EAE0-44A6-8774-FFD645390401|E3E379DF-F4C6-4180-9B81-6769533ABE47)`)
	csprojWebRef  = regexp.MustCompile(`(?i)<Reference\s+Include="(?:System\.Web(?:\.Http|\.Mvc|\.Http\.WebHost)?|Microsoft\.AspNet\.[^",]*)\s*[",]`)
	csprojTestRef = regexp.MustCompile(`(?i)Microsoft\.VisualStudio\.QualityTools\.UnitTestFramework|Microsoft\.VisualStudio\.TestPlatform|<Reference\s+Include="nunit\.framework|<Reference\s+Include="xunit\.|3AC096D0-A1C2-E12C-1390-A8335801FDAB`)
	csprojSdkAttr = regexp.MustCompile(`<Project\b[^>]*\bSdk\s*=|<Sdk\s+Name\s*=|<Import\b[^>]*\bSdk\s*=`)
)

func applyCsproj(f *FileCtx) []truth.Fact {
	var codeLines []string // comment-free text per line
	inComment := false
	for _, l := range f.Lines {
		s := csprojComment.ReplaceAllString(l.Raw, "")
		if inComment {
			if i := strings.Index(s, "-->"); i >= 0 {
				s, inComment = s[i+3:], false
			} else {
				s = ""
			}
		}
		if i := strings.Index(s, "<!--"); i >= 0 {
			s, inComment = s[:i], true
		}
		codeLines = append(codeLines, s)
	}
	web, exe, isTest := false, false, false
	sdkStyle, legacyWeb := false, false
	asmLine, asmName := -1, ""
	for i, s := range codeLines {
		switch {
		case strings.Contains(s, `Sdk="Microsoft.NET.Sdk.Web"`), strings.Contains(s, `Sdk="Microsoft.NET.Sdk.Worker"`):
			web = true
		case strings.Contains(s, "<OutputType>Exe</OutputType>"):
			exe = true
		}
		if strings.Contains(s, "Microsoft.NET.Test.Sdk") || strings.Contains(s, "<IsTestProject>true<") || csprojTestRef.MatchString(s) {
			isTest = true
		}
		if csprojSdkAttr.MatchString(s) {
			sdkStyle = true
		}
		if csprojWebGuid.MatchString(s) || csprojWebRef.MatchString(s) {
			legacyWeb = true
		}
		if m := csprojAssembly.FindStringSubmatch(s); m != nil && asmLine < 0 {
			asmLine, asmName = i, m[1]
		}
	}
	// A non-SDK project is a service when it is an ASP.NET Framework web
	// project (web guid or System.Web reference); a plain library is not.
	if !sdkStyle && legacyWeb {
		web = true
	}
	if !(web || exe) || isTest || len(f.Lines) == 0 {
		return nil
	}
	stem := strings.TrimSuffix(baseName(f.Path), path.Ext(f.Path))
	name, line := stem, f.Lines[0]
	if asmLine >= 0 {
		name, line = asmName, f.Lines[asmLine]
		if name == "$(MSBuildProjectName)" {
			name = stem
		}
	}
	if fc, ok := serviceFact("csharp.web_sdk", f, "own", name, "", "", line); ok {
		return []truth.Fact{fc}
	}
	return nil
}

// ---- nest ----

// genericServiceDir lists directory names that say what a folder is for, not
// which service lives in it; they never name a service.
var genericServiceDir = map[string]bool{
	"src": true, "app": true, "apps": true, "lib": true, "server": true, "docker": true,
	"dockerfiles": true, "deploy": true, "build": true, "ci": true, "infra": true,
}

func applyNestBootstrap(f *FileCtx) []truth.Fact {
	var at *lex.Line
	for i := range f.Lines {
		if strings.Contains(f.Lines[i].Code, "NestFactory.create(") {
			at = &f.Lines[i]
			break
		}
	}
	if at == nil {
		return nil
	}
	segs := strings.Split(f.Dir, "/")
	name, pkgDir := "", ""
	for d := f.Dir; ; d = path.Dir(d) {
		if b, ok := f.Tree.ReadFile(path.Join(d, "package.json")); ok {
			var pj struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(b, &pj) == nil && strings.TrimSpace(pj.Name) != "" {
				name, pkgDir = strings.TrimSpace(pj.Name), d
				break
			}
		}
		if d == "." || d == "/" || d == "" {
			break
		}
	}
	switch {
	case name != "" && pkgDir == "." && len(segs) >= 2 && segs[0] == "apps":
		name = segs[1] // Nest monorepo: the root package.json names the workspace, not the app
	case name != "":
		name = name[strings.LastIndex(name, "/")+1:] // strip the npm scope
	case f.Dir != "." && !genericServiceDir[strings.ToLower(segs[0])]:
		name = segs[0]
	default:
		return nil
	}
	if fc, ok := serviceFact("nest.bootstrap", f, "own", name, "", "", *at); ok {
		return []truth.Fact{fc}
	}
	return nil
}

// ---- python entry ----

func isDockerfilePath(p string) bool {
	b := strings.ToLower(baseName(p))
	return b == "dockerfile" || strings.HasPrefix(b, "dockerfile.")
}

var (
	dockerCmd    = regexp.MustCompile(`(?i)^\s*(?:CMD|ENTRYPOINT)\b(.*)$`)
	pythonServer = regexp.MustCompile(`\b(?:uvicorn|gunicorn)\b|\bmanage\.py\b`)
)

// envDockerSuffix lists Dockerfile.<suffix> values that name an environment or
// build flavour rather than a service.
var envDockerSuffix = map[string]bool{
	"prod": true, "production": true, "dev": true, "development": true,
	"staging": true, "stage": true, "test": true, "testing": true, "local": true,
	"base": true, "ci": true, "debug": true, "release": true, "build": true,
	"builder": true, "runtime": true, "alpine": true, "slim": true,
}

func applyPythonEntry(f *FileCtx) []truth.Fact {
	if f.Dir == "." {
		return nil // a root Dockerfile cannot name the service
	}
	for _, l := range f.Lines {
		if m := dockerCmd.FindStringSubmatch(l.Code); m != nil && pythonServer.MatchString(m[1]) {
			name := path.Base(f.Dir)
			if genericServiceDir[strings.ToLower(name)] {
				// docker/Dockerfile.api: the suffix names the service.
				b := baseName(f.Path)
				i := strings.Index(b, ".")
				if i < 0 || i == len(b)-1 || !strings.EqualFold(b[:i], "dockerfile") {
					return nil
				}
				name = b[i+1:]
				if envDockerSuffix[strings.ToLower(name)] {
					return nil // docker/Dockerfile.prod names an environment, not a service
				}
			}
			if fc, ok := serviceFact("python.entry", f, "own", name, "", "", l); ok {
				return []truth.Fact{fc}
			}
			return nil
		}
	}
	return nil
}
