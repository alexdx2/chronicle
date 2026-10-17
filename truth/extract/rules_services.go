package extract

import (
	"bytes"
	"encoding/json"
	"io"
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

func svcLineAt(f *FileCtx, n int) (lex.Line, bool) {
	if n < 1 || n > len(f.Lines) {
		return lex.Line{}, false
	}
	return f.Lines[n-1], true
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
		key, svc := services.Content[i], services.Content[i+1]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		l, ok := svcLineAt(f, key.Line)
		if !ok {
			continue
		}
		image := ""
		if n := yamlMap(svc, "image"); n != nil && n.Kind == yaml.ScalarNode {
			image = n.Value
		}
		kind, tech := "external", ""
		switch {
		case yamlMap(svc, "build") != nil:
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
	dec := yaml.NewDecoder(bytes.NewReader(f.Raw))
	var out []truth.Fact
	for {
		var doc yaml.Node
		if err := dec.Decode(&doc); err != nil {
			if err != io.EOF {
				return out // a templated document: keep what parsed so far
			}
			break
		}
		root := yamlDoc(&doc)
		if k := yamlMap(root, "kind"); k == nil || !k8sWorkloads[k.Value] {
			continue
		}
		name := yamlMap(yamlMap(root, "metadata"), "name")
		if name == nil || name.Kind != yaml.ScalarNode {
			continue
		}
		if l, ok := svcLineAt(f, name.Line); ok {
			if fc, ok := serviceFact("k8s.workload", f, "own", name.Value, "", "", l); ok {
				out = append(out, fc)
			}
		}
	}
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
	if l, ok := svcLineAt(f, name.Line); ok {
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
	tfAnyAttr = regexp.MustCompile(`^\s*(function_name|name|family)\s*=\s*"([^"]*)"`)
)

func applyTerraformCompute(f *FileCtx) []truth.Fact {
	var out []truth.Fact
	for i := 0; i < len(f.Lines); i++ {
		m := tfCompute.FindStringSubmatch(f.Lines[i].Code)
		if m == nil {
			continue
		}
		res := f.Lines[i]
		depth := strings.Count(res.Code, "{") - strings.Count(res.Code, "}")
		j := i + 1
		for ; j < len(f.Lines) && depth > 0; j++ {
			l := f.Lines[j]
			if a := tfAnyAttr.FindStringSubmatch(l.Code); a != nil && depth == 1 && a[1] == tfNameKey[m[1]] {
				if fc, ok := serviceFact("terraform.compute", f, "own", a[2], "", "", l, evidence(f, res)); ok {
					out = append(out, fc)
				}
			}
			depth += strings.Count(l.Code, "{") - strings.Count(l.Code, "}")
		}
		i = j - 1
	}
	return out
}

// ---- cdk ----

var (
	cdkFnTS    = regexp.MustCompile(`new\s+(?:\w+\.)?(?:Function|NodejsFunction|DockerImageFunction|PythonFunction)\(\s*\w+\s*,\s*['"]([^'"]+)['"]`)
	cdkFnPy    = regexp.MustCompile(`\b\w*lambda\w*\.(?:Function|DockerImageFunction|PythonFunction)\(\s*\w+\s*,\s*['"]([^'"]+)['"]`)
	cdkNameTS  = regexp.MustCompile(`\bfunctionName\s*:\s*(['"])([^'"]*)['"]\s*[,}]?`)
	cdkNamePy  = regexp.MustCompile(`\bfunction_name\s*=\s*(['"])([^'"]*)['"]`)
	cdkNameAny = regexp.MustCompile(`\b(?:functionName\s*:|function_name\s*=)`)
	cdkNextNew = regexp.MustCompile(`\bnew\s|\b\w*lambda\w*\.(?:Function|DockerImageFunction|PythonFunction)\(`)
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
		text := codeWindow(f.Lines, i, 7)
		loc := re.FindStringSubmatchIndex(text)
		if loc == nil || loc[0] >= len(l.Code) {
			continue
		}
		id := text[loc[2]:loc[3]]
		rest := text[loc[1]:]
		if n := cdkNextNew.FindStringIndex(rest); n != nil {
			rest = rest[:n[0]] // the next construct is not ours
		}
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
	asmLine, asmName := -1, ""
	for i, s := range codeLines {
		switch {
		case strings.Contains(s, `Sdk="Microsoft.NET.Sdk.Web"`):
			web = true
		case strings.Contains(s, "<OutputType>Exe</OutputType>"):
			exe = true
		}
		if strings.Contains(s, "Microsoft.NET.Test.Sdk") || strings.Contains(s, "<IsTestProject>true<") {
			isTest = true
		}
		if m := csprojAssembly.FindStringSubmatch(s); m != nil && asmLine < 0 {
			asmLine, asmName = i, m[1]
		}
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
	case f.Dir != ".":
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

func applyPythonEntry(f *FileCtx) []truth.Fact {
	if f.Dir == "." {
		return nil // a root Dockerfile cannot name the service
	}
	for _, l := range f.Lines {
		if m := dockerCmd.FindStringSubmatch(l.Code); m != nil && pythonServer.MatchString(m[1]) {
			if fc, ok := serviceFact("python.entry", f, "own", path.Base(f.Dir), "", "", l); ok {
				return []truth.Fact{fc}
			}
			return nil
		}
	}
	return nil
}
