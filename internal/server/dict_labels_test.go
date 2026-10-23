package server

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"exe/internal/dictlabels"
)

func TestDictLabelSchemaAndPromptByLanguage(t *testing.T) {
	for _, src := range dictSources {
		for _, dst := range dictTargets {
			var schema map[string]any
			if err := json.Unmarshal([]byte(dictEntrySchema(src.Code)), &schema); err != nil {
				t.Fatal(err)
			}
			pos := schema["properties"].(map[string]any)["senses"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)["pos"].(map[string]any)
			if src.Code != "en" {
				if pos["pattern"] != nil || dictEntrySchema(src.Code) != dictSchema {
					t.Fatalf("changed %s label conventions", src.Code)
				}
				continue
			}
			if pos["pattern"] != dictlabels.Pattern {
				t.Fatalf("missing English constraint for %s", dst.Code)
			}
			re := regexp.MustCompile(pos["pattern"].(string))
			for _, label := range []string{"n.", "vt., vi.", "n. [C, U]", "proper n.", "n. [C, usu. sing.]"} {
				if !re.MatchString(label) {
					t.Errorf("schema rejects %q", label)
				}
			}
			for _, label := range []string{"名", "n.（不可数）", "名（可数）", "unknown", ""} {
				if re.MatchString(label) {
					t.Errorf("schema accepts %q", label)
				}
			}
			prompt := dictPrompt(src, dst, "empire")
			if !strings.Contains(prompt, dictlabels.Instructions()) || !strings.Contains(prompt, "Except senses[].pos") {
				t.Fatalf("missing prompt exception for %s", dst.Code)
			}
			if strings.Contains(prompt, "learner's dictionary of English abbreviates it") {
				t.Fatal("ambiguous label instruction remains")
			}
		}
	}
}

func TestDictParseNormalizesAndRejectsLabels(t *testing.T) {
	for _, label := range []string{"名", "名（不可数）", "动（及物、不及物）"} {
		answer := strings.Replace(dictAnswer, `"pos":"n."`, `"pos":"`+label+`"`, 1)
		e, raw, err := dictParse("en", answer)
		want, _ := dictlabels.Normalize(label)
		if err != nil || e.Senses[0].POS != want || strings.Contains(string(raw), `"pos":"`+label+`"`) {
			t.Fatalf("normalized %q: %s, %v", label, raw, err)
		}
		// The same source-language rule must not rewrite Latin's labels.
		e, _, err = dictParse("la", answer)
		if err != nil || e.Senses[0].POS != label {
			t.Fatal("changed a non-English label")
		}
	}
	answer := strings.Replace(dictAnswer, `"pos":"n."`, `"pos":"名（未知）"`, 1)
	if _, _, err := dictParse("en", answer); err == nil {
		t.Fatal("accepted an unfamiliar label")
	}
	if _, _, err := dictParse("en", `{"found":false,"senses":[],"suggestions":["word"]}`); err != nil {
		t.Fatal("rejected a not-found result", err)
	}
}
