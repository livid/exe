package dictlabels

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"n.": "n.", "名": "n.", "形": "adj.", "副": "adv.", "动": "v.",
		"名词": "n.", "形容词": "adj.", "可数名词": "n. [C]", "专名": "proper n.",
		"动（及物）": "vt.", "动（不及物）": "vi.", "及物动": "vt.", "及物动词": "vt.",
		"不及物动词": "vi.", "动（及物、不及物）": "vt., vi.", "动（不及物、及物）": "vi., vt.",
		"名（可数）": "n. [C]", "名（不可数）": "n. [U]", "名（可数或不可数）": "n. [C, U]",
		"n.（可数、不可数）": "n. [C, U]", "n.［不可数］": "n. [U]", "名（通常不可数）": "n. [usu. U]",
		"名（可数，常用单数）": "n. [C, usu. sing.]", "n.（可数，通常用单数）": "n. [C, usu. sing.]",
		"名（单数）": "n. [sing.]", "名（复数）": "n. [pl.]", "n. [C, U]": "n. [C, U]",
		"vt. & vi.": "vt., vi.", "v.; n.": "v., n.", "n./v.": "n., v.", "v./adj.": "v., adj.",
		"int.": "interj.", "感叹用语": "interj.", "动词短语": "phr. v.", "连": "conj.", "介": "prep.",
		"n. [C], vt.": "n. [C], vt.", "名（可数）, 动（及物）": "n. [C], vt.",
	}
	for input, want := range cases {
		t.Run(input, func(t *testing.T) {
			got, err := Normalize(input)
			if err != nil || got != want {
				t.Fatalf("Normalize(%q) = %q, %v; want %q", input, got, err, want)
			}
			if again, err := Normalize(got); err != nil || again != got {
				t.Fatalf("not idempotent: %q, %v", again, err)
			}
		})
	}
	for _, input := range []string{"", "unknown", "名（古义）", "n. [rare]", "名（可数", "n. [C]]", "n.,", "名（可数）extra"} {
		if got, err := Normalize(input); err == nil {
			t.Errorf("silently accepted %q as %q", input, got)
		}
	}
}

func TestRepairPreservesEverythingExceptLabels(t *testing.T) {
	raw := []byte(`{"headword":"empire","future":{"integer":9007199254740993},"senses":[{"pos":"名","gloss":"帝国","definition":"原文。","future":"retained"},{"pos":"名（可数，常用单数）","gloss":"统治","definition":"unchanged"}],"examples":[{"text":"An empire.","translation":"一个帝国。"}]}`)
	got, changes, err := Repair(raw)
	if err != nil || len(changes) != 2 || changes[0] != (Change{1, "名", "n."}) {
		t.Fatalf("changes = %v, error = %v", changes, err)
	}
	var before, after map[string]json.RawMessage
	json.Unmarshal(raw, &before)
	json.Unmarshal(got, &after)
	var bs, as []map[string]json.RawMessage
	json.Unmarshal(before["senses"], &bs)
	json.Unmarshal(after["senses"], &as)
	for i := range bs {
		delete(bs[i], "pos")
		delete(as[i], "pos")
		if !reflect.DeepEqual(bs[i], as[i]) {
			t.Fatalf("changed prose or unknown fields: %v != %v", bs[i], as[i])
		}
	}
	delete(before, "senses")
	delete(after, "senses")
	if !reflect.DeepEqual(before, after) {
		t.Fatal("changed another field")
	}
	again, changes, err := Repair(got)
	if err != nil || len(changes) != 0 || !bytes.Equal(got, again) {
		t.Fatal("repair is not byte-identical on the second pass")
	}
	bad := bytes.Replace(raw, []byte("名（可数，常用单数）"), []byte("名（未知修饰）"), 1)
	if changed, changes, err := Repair(bad); err == nil || changed != nil || changes != nil {
		t.Fatal("partially repaired an entry containing an unknown qualifier")
	}
}
