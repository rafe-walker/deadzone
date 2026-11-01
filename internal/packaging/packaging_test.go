// Package packaging has no code; its tests keep the Home Assistant app
// files, the blueprint and the version pins consistent with each other.
package packaging

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const root = "../.."

func readYAML(t *testing.T, rel string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		t.Fatalf("%s: %v", rel, err)
	}
	return out
}

func TestTranslationsCoverEveryOption(t *testing.T) {
	cfg := readYAML(t, "addon/deadzone/config.yaml")
	tr := readYAML(t, "addon/deadzone/translations/en.yaml")
	options, _ := cfg["options"].(map[string]any)
	conf, _ := tr["configuration"].(map[string]any)
	if len(options) == 0 || len(conf) == 0 {
		t.Fatalf("options=%d translations=%d", len(options), len(conf))
	}
	for key := range options {
		entry, ok := conf[key].(map[string]any)
		if !ok {
			t.Errorf("translations/en.yaml lacks configuration.%s", key)
			continue
		}
		name, _ := entry["name"].(string)
		desc, _ := entry["description"].(string)
		if strings.TrimSpace(name) == "" || strings.TrimSpace(desc) == "" {
			t.Errorf("configuration.%s needs both name and description", key)
		}
	}
	for key := range conf {
		if _, ok := options[key]; !ok {
			t.Errorf("translation for unknown option %s", key)
		}
	}
	// Every schema key must also be an option (the Supervisor rejects otherwise).
	schema, _ := cfg["schema"].(map[string]any)
	for key := range schema {
		if _, ok := options[key]; !ok {
			t.Errorf("schema key %s has no default in options", key)
		}
	}
	network, _ := tr["network"].(map[string]any)
	ports, _ := cfg["ports"].(map[string]any)
	for port := range ports {
		if _, ok := network[port]; !ok {
			t.Errorf("translations lack network.%s", port)
		}
	}
	if _, ok := cfg["ports_description"]; !ok {
		t.Error("ports_description must stay for older Supervisors")
	}
}

func TestVersionPinsAgree(t *testing.T) {
	cfg := readYAML(t, "addon/deadzone/config.yaml")
	version, _ := cfg["version"].(string)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(version) {
		t.Fatalf("config.yaml version %q", version)
	}
	build := readYAML(t, "addon/deadzone/build.yaml")
	args, _ := build["args"].(map[string]any)
	if ref, _ := args["DEADZONE_REF"].(string); ref != "v"+version {
		t.Errorf("build.yaml DEADZONE_REF=%q, config.yaml version=%q", ref, version)
	}
	changelog, err := os.ReadFile(filepath.Join(root, "addon/deadzone/CHANGELOG.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(changelog), "\n## "+version+"\n") {
		t.Errorf("CHANGELOG.md has no section for %s", version)
	}
	mainGo, err := os.ReadFile(filepath.Join(root, "cmd/deadzone/main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mainGo), `version = "`+version+`"`) {
		t.Errorf("cmd/deadzone/main.go default version should be %q", version)
	}
	// image: stays commented out until the GHCR-public procedure is done.
	raw, _ := os.ReadFile(filepath.Join(root, "addon/deadzone/config.yaml"))
	if _, has := cfg["image"]; has {
		if _, err := os.Stat(filepath.Join(root, ".github/workflows/addon-image.yml")); err != nil {
			t.Error("image: is set but the image workflow is missing")
		}
	} else if !strings.Contains(string(raw), `# image: "ghcr.io/`) {
		t.Error("config.yaml should document the image: switch")
	}
}

func TestStoreAssetsPresent(t *testing.T) {
	for _, f := range []string{"addon/deadzone/icon.png", "addon/deadzone/logo.png", "addon/deadzone/DOCS.md", "addon/deadzone/README.md"} {
		if _, err := os.Stat(filepath.Join(root, f)); err != nil && !strings.HasSuffix(f, "README.md") {
			t.Errorf("%s: %v", f, err)
		}
	}
	png := func(name string) (w, h int) {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(b[:8]) != "\x89PNG\r\n\x1a\n" {
			t.Fatalf("%s is not a PNG", name)
		}
		// IHDR width/height are big-endian at offsets 16 and 20.
		be := func(o int) int { return int(b[o])<<24 | int(b[o+1])<<16 | int(b[o+2])<<8 | int(b[o+3]) }
		return be(16), be(20)
	}
	if w, h := png("addon/deadzone/icon.png"); w != 128 || h != 128 {
		t.Errorf("icon.png is %dx%d, want 128x128", w, h)
	}
	if w, h := png("addon/deadzone/logo.png"); w != 250 || h != 100 {
		t.Errorf("logo.png is %dx%d, want 250x100", w, h)
	}
	if w, h := png("web/icon-192.png"); w != 192 || h != 192 {
		t.Errorf("icon-192.png is %dx%d", w, h)
	}
	if w, h := png("web/icon-512.png"); w != 512 || h != 512 {
		t.Errorf("icon-512.png is %dx%d", w, h)
	}
}

func TestBlueprintShape(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "blueprints/automation/deadzone/call_ok_notify.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// !input is a Home Assistant tag; neutralise it for the parser.
	src := regexp.MustCompile(`!input (\S+)`).ReplaceAllString(string(b), `"input:$1"`)
	var bp map[string]any
	if err := yaml.Unmarshal([]byte(src), &bp); err != nil {
		t.Fatal(err)
	}
	meta, _ := bp["blueprint"].(map[string]any)
	if meta["domain"] != "automation" {
		t.Fatalf("domain=%v", meta["domain"])
	}
	inputs, _ := meta["input"].(map[string]any)
	for _, in := range []string{"call_ok_entity", "notify_service", "settle_seconds", "after", "before", "notify_on_go"} {
		if _, ok := inputs[in]; !ok {
			t.Errorf("blueprint input %s missing", in)
		}
	}
	ns, _ := inputs["notify_service"].(map[string]any)
	if ns["default"] != "notify.persistent_notification" {
		t.Errorf("notify_service default %v", ns["default"])
	}
	// Every referenced input must be declared.
	for _, m := range regexp.MustCompile(`"input:(\w+)"`).FindAllStringSubmatch(src, -1) {
		if _, ok := inputs[m[1]]; !ok {
			t.Errorf("!input %s is not declared", m[1])
		}
	}
	triggers, _ := bp["triggers"].([]any)
	if len(triggers) != 2 {
		t.Fatalf("triggers=%d", len(triggers))
	}
	for _, tr := range triggers {
		m, _ := tr.(map[string]any)
		if id, ok := m["id"].(string); !ok || id == "" {
			t.Errorf("trigger id must be a non-empty string (YAML 'no' is a bool): %v", m["id"])
		}
	}
	for _, k := range []string{"conditions", "actions", "mode"} {
		if _, ok := bp[k]; !ok {
			t.Errorf("blueprint lacks %s", k)
		}
	}
}

func TestWebManifestValid(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, "web/manifest.webmanifest"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"name", "short_name", "start_url", "display", "icons", "theme_color", "background_color"} {
		if _, ok := m[k]; !ok {
			t.Errorf("manifest lacks %s", k)
		}
	}
}

func TestImageWorkflowUsesComposableBuilder(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(root, ".github/workflows/addon-image.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	for _, want := range []string{
		"home-assistant/builder/actions/prepare-multi-arch-matrix@",
		"home-assistant/builder/actions/build-image@",
		"home-assistant/builder/actions/publish-multi-arch-manifest@",
		"packages: write", "id-token: write",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow lacks %q", want)
		}
	}
	if regexp.MustCompile(`uses:\s*home-assistant/builder@`).MatchString(wf) {
		t.Error("the all-in-one home-assistant/builder action is deprecated")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["jobs"].(map[string]any)["manifest"]; !ok {
		t.Error("workflow needs the manifest job")
	}
}
