package cf

import (
	"reflect"
	"testing"
)

func ingressRule(h, svc string) map[string]any {
	m := map[string]any{"service": svc}
	if h != "" {
		m["hostname"] = h
	}
	return m
}

func ingressHosts(rules []any) []string {
	out := make([]string, 0, len(rules))
	for _, r := range rules {
		h, _ := ruleHost(r)
		out = append(out, h)
	}
	return out
}

func TestInsertIngressGoesBeforeWildcard(t *testing.T) {
	rules := []any{ingressRule("cid.v2ex.pro", "x"), ingressRule("*.v2ex.pro", "y"), ingressRule("", "http_status:404")}
	got := ingressHosts(insertIngress(rules, ingressRule("ipfs.v2ex.pro", "x")))
	want := []string{"cid.v2ex.pro", "ipfs.v2ex.pro", "*.v2ex.pro", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInsertIngressGoesBeforeCatchAllWithoutWildcard(t *testing.T) {
	rules := []any{ingressRule("a.example", "x"), ingressRule("", "http_status:404")}
	got := ingressHosts(insertIngress(rules, ingressRule("b.example", "x")))
	want := []string{"a.example", "b.example", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestInsertIngressIntoEmptyKeepsCatchAllLast(t *testing.T) {
	got := ingressHosts(ensureCatchAll(insertIngress(nil, ingressRule("a.example", "x"))))
	want := []string{"a.example", ""}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestHoistIngressMovesOurShadowedRuleOnly(t *testing.T) {
	rules := []any{
		ingressRule("cid.v2ex.pro", "x"),
		ingressRule("*.v2ex.pro", "y"),
		ingressRule("theirs.v2ex.pro", "z"),
		ingressRule("ipfs.v2ex.pro", "x"),
		ingressRule("", "http_status:404"),
	}
	got, moved := hoistIngress(rules, map[string]string{"cid.v2ex.pro": "x", "ipfs.v2ex.pro": "x"})
	want := []string{"cid.v2ex.pro", "ipfs.v2ex.pro", "*.v2ex.pro", "theirs.v2ex.pro", ""}
	if !reflect.DeepEqual(ingressHosts(got), want) {
		t.Fatalf("got %v, want %v", ingressHosts(got), want)
	}
	if !reflect.DeepEqual(moved, []string{"ipfs.v2ex.pro"}) {
		t.Fatalf("moved %v, want [ipfs.v2ex.pro]", moved)
	}
}

func TestHoistIngressLeavesGoodOrderAlone(t *testing.T) {
	rules := []any{ingressRule("a.example", "x"), ingressRule("*.example", "y"), ingressRule("", "http_status:404")}
	got, moved := hoistIngress(rules, map[string]string{"a.example": "x"})
	if len(moved) != 0 || !reflect.DeepEqual(ingressHosts(got), []string{"a.example", "*.example", ""}) {
		t.Fatalf("moved %v, order %v", moved, ingressHosts(got))
	}
}
