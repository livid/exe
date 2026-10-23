// Package dictlabels defines the English dictionary's part-of-speech labels.
// Generation and cache repair share these rules; other source languages keep
// their own dictionary conventions.
package dictlabels

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var bases = []string{"n.", "proper n.", "v.", "vt.", "vi.", "adj.", "adv.",
	"pron.", "det.", "art.", "prep.", "conj.", "interj.", "num.", "aux.",
	"modal v.", "phr. v.", "phr.", "abbr.", "pref.", "suff.", "symb."}
var qualifiers = []string{"C", "U", "sing.", "pl.", "usu. C", "usu. U", "usu. sing.", "usu. pl."}

func alternatives(values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = regexp.QuoteMeta(v)
	}
	return "(" + strings.Join(quoted, "|") + ")"
}

// Pattern is used by both the structured-output schema and the save-time check.
// Each member of a compound label may carry its own grammatical qualifiers.
var Pattern = func() string {
	q := alternatives(qualifiers)
	part := alternatives(bases) + `( \[` + q + `(, ` + q + `)*\])?`
	return "^" + part + "(, " + part + ")*$"
}()

var canonical = regexp.MustCompile(Pattern)

func Instructions() string {
	return "For English entries, senses[].pos is an exception to translated labels: " +
		"use only these English abbreviations, never Chinese, Japanese or Korean labels: " +
		strings.Join(bases, ", ") + ". Use vt. for a transitive verb and vi. for an intransitive verb. " +
		"When needed, append square-bracket qualifiers chosen from " + strings.Join(qualifiers, ", ") +
		" (C = countable, U = uncountable, sing. = singular, pl. = plural, usu. = usually). " +
		"Separate combined parts of speech or qualifiers with a comma and a space: " +
		"n. [C], n. [C, U], vt., vi. Keep other usage explanations in the definition, " +
		"in the reader's language. For example, empire has pos \"n.\", not \"名\"; " +
		"an uncountable noun has pos \"n. [U]\", not \"名（不可数）\"."
}

var aliases = map[string]string{
	"名": "n.", "名词": "n.", "名詞": "n.", "noun": "n.",
	"专名": "proper n.", "专有名词": "proper n.", "proper noun": "proper n.",
	"动": "v.", "动词": "v.", "verb": "v.",
	"及物动": "vt.", "及物动词": "vt.", "不及物动": "vi.", "不及物动词": "vi.",
	"形": "adj.", "形容词": "adj.", "adjective": "adj.",
	"副": "adv.", "副词": "adv.", "adverb": "adv.",
	"代": "pron.", "代词": "pron.", "限定词": "det.", "冠": "art.", "冠词": "art.",
	"介": "prep.", "介词": "prep.", "连": "conj.", "连词": "conj.",
	"int.": "interj.", "感叹词": "interj.", "感叹用语": "interj.",
	"数": "num.", "数词": "num.", "助动词": "aux.", "情态动词": "modal v.",
	"动词短语": "phr. v.", "短语动词": "phr. v.", "phrasal v.": "phr. v.",
	"短语": "phr.", "缩略词": "abbr.", "前缀": "pref.", "后缀": "suff.",
	"可数名词": "n. [C]", "不可数名词": "n. [U]",
}

var qualifierAliases = map[string]string{
	"可数": "C", "不可数": "U", "单数": "sing.", "复数": "pl.",
	"通常可数": "usu. C", "通常不可数": "usu. U", "多为不可数": "usu. U",
	"常用单数": "usu. sing.", "通常用单数": "usu. sing.", "通常单数": "usu. sing.",
	"常用复数": "usu. pl.", "通常用复数": "usu. pl.", "通常复数": "usu. pl.",
}

// Normalize translates only known labels. Unknown text is an error, never a
// guessed part of speech or a discarded qualifier.
func Normalize(label string) (string, error) {
	if canonical.MatchString(label) {
		return label, nil
	}
	s := strings.NewReplacer("（", "[", "）", "]", "［", "[", "］", "]",
		"(", "[", ")", "]", "，", ",", "、", ",", "；", ";").Replace(label)
	// Split compound labels only outside grammatical qualifiers.
	var parts []string
	start, depth := 0, 0
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',', ';', '/', '&':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
		if depth < 0 || depth > 1 {
			return "", fmt.Errorf("unrecognized part-of-speech label %q", label)
		}
	}
	if depth != 0 {
		return "", fmt.Errorf("unrecognized part-of-speech label %q", label)
	}
	parts = append(parts, s[start:])
	for i, part := range parts {
		part = strings.Join(strings.Fields(part), " ")
		base, qualifier, hasQualifier := strings.Cut(part, "[")
		base = strings.TrimSpace(base)
		if alias, ok := aliases[base]; ok {
			base = alias
		}
		if hasQualifier {
			if !strings.HasSuffix(qualifier, "]") {
				return "", fmt.Errorf("unrecognized part-of-speech label %q", label)
			}
			qualifier = strings.TrimSuffix(qualifier, "]")
			qualifier = strings.NewReplacer("或", ",", "和", ",", ";", ",").Replace(qualifier)
			q := strings.Split(qualifier, ",")
			for j := range q {
				q[j] = strings.TrimSpace(q[j])
				if alias, ok := qualifierAliases[q[j]]; ok {
					q[j] = alias
				}
			}
			if base == "v." && (strings.Join(q, ",") == "及物" || strings.Join(q, ",") == "不及物" ||
				strings.Join(q, ",") == "及物,不及物" || strings.Join(q, ",") == "不及物,及物") {
				for j := range q {
					if q[j] == "及物" {
						q[j] = "vt."
					} else {
						q[j] = "vi."
					}
				}
				base = strings.Join(q, ", ")
			} else {
				base += " [" + strings.Join(q, ", ") + "]"
			}
		}
		parts[i] = base
	}
	result := strings.Join(parts, ", ")
	if !canonical.MatchString(result) {
		return "", fmt.Errorf("unrecognized part-of-speech label %q", label)
	}
	return result, nil
}

type Change struct {
	Sense int    `json:"sense"` // one-based, as displayed in Dict
	From  string `json:"from"`
	To    string `json:"to"`
}

// Repair changes only senses[].pos, retaining unknown JSON fields as well as
// dictionary prose. An unrecognized label leaves the entire entry untouched.
func Repair(raw []byte) ([]byte, []Change, error) {
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entry); err != nil {
		return nil, nil, err
	}
	var senses []map[string]json.RawMessage
	if err := json.Unmarshal(entry["senses"], &senses); err != nil {
		return nil, nil, err
	}
	var changes []Change
	for i, sense := range senses {
		var old string
		if err := json.Unmarshal(sense["pos"], &old); err != nil {
			return nil, nil, fmt.Errorf("sense %d: %w", i+1, err)
		}
		pos, err := Normalize(old)
		if err != nil {
			return nil, nil, fmt.Errorf("sense %d: %w", i+1, err)
		}
		if pos != old {
			changes = append(changes, Change{i + 1, old, pos})
			sense["pos"], _ = json.Marshal(pos)
		}
	}
	if len(changes) == 0 {
		return raw, nil, nil
	}
	entry["senses"], _ = json.Marshal(senses)
	out, err := json.Marshal(entry)
	return out, changes, err
}
