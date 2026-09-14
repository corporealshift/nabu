package memory

import (
	"strings"
	"testing"
)

func corpus() []Memory {
	return []Memory{
		{Name: "rancher-desktop-docker", Description: "Rancher Desktop, not Docker Desktop",
			Body: "Launch the Rancher daemon before running compose. Port 8080 is taken."},
		{Name: "farthing-shared-checkout", Description: "the farthing checkout is shared",
			Body: "Other terminals use the same working tree, so re-check the branch before you commit."},
		{Name: "nabu-project", Description: "the owner's Go agent harness",
			Body: "Read the spec before changing anything. The microkernel boundary is a constraint."},
	}
}

func TestNameMatchOutranksBodyMatch(t *testing.T) {
	// "spec" appears in nabu-project's body only; "rancher" is in the other's
	// name. A memory named for its subject should win a query about it.
	got := Rank("rancher", corpus(), 5)
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].Name != "rancher-desktop-docker" {
		t.Errorf("top hit = %s, want the memory named for the query", got[0].Name)
	}

	got = Rank("microkernel", corpus(), 5)
	if len(got) != 1 || got[0].Name != "nabu-project" {
		t.Fatalf("a body-only term did not match: %+v", got)
	}
}

func TestEqualTermRanksNameAboveBody(t *testing.T) {
	mems := []Memory{
		{Name: "compose", Description: "d", Body: "unrelated words entirely"},
		{Name: "other", Description: "d", Body: "compose"},
	}
	got := Rank("compose", mems, 5)
	if len(got) != 2 {
		t.Fatalf("want both matched, got %d", len(got))
	}
	if got[0].Name != "compose" {
		t.Errorf("order = %s then %s, want the name match first", got[0].Name, got[1].Name)
	}
}

func TestTermInEveryDocumentDoesNotSeparate(t *testing.T) {
	mems := []Memory{
		{Name: "a", Body: "the thing the"},
		{Name: "b", Body: "the other the"},
		{Name: "c", Body: "the third the"},
	}
	// "the" is in all three, so it carries no information: it must not pull
	// one memory above another.
	got := Rank("the", mems, 5)
	if len(got) != 3 {
		t.Fatalf("got %d matches, want all three", len(got))
	}
	for _, m := range got[1:] {
		if m.Score != got[0].Score {
			t.Fatalf("a universal term separated the documents: %v vs %v", got[0], m)
		}
	}
	// Paired with a rare term, the rare term decides.
	got = Rank("the third", mems, 5)
	if len(got) == 0 || got[0].Name != "c" {
		t.Fatalf("the rare term did not decide the ranking: %+v", got)
	}
}

func TestLengthDoesNotBuyRank(t *testing.T) {
	long := strings.Repeat("filler words about nothing in particular ", 60)
	mems := []Memory{
		{Name: "short", Body: "docker"},
		{Name: "long", Body: "docker " + long},
	}
	got := Rank("docker", mems, 5)
	if len(got) != 2 {
		t.Fatalf("want both, got %d", len(got))
	}
	if got[0].Name != "short" {
		t.Errorf("the long document outranked the short one on the same single match")
	}
}

func TestTokenisation(t *testing.T) {
	mems := []Memory{{Name: "a", Body: "Run docker compose up"}}
	for _, q := range []string{"DOCKER", "docker-compose", "docker_compose", "  docker  "} {
		if got := Rank(q, mems, 5); len(got) == 0 {
			t.Errorf("query %q matched nothing", q)
		}
	}
}

func TestEmptyAndUnmatchedQueries(t *testing.T) {
	mems := corpus()
	for _, q := range []string{"", "   ", "!!!"} {
		if got := Rank(q, mems, 5); len(got) != 0 {
			t.Errorf("query %q returned %d results, want none", q, len(got))
		}
	}
	if got := Rank("kubernetes", mems, 5); len(got) != 0 {
		t.Errorf("an unmatched query returned %+v, want nothing", got)
	}
	if got := Rank("docker", nil, 5); len(got) != 0 {
		t.Errorf("an empty corpus returned %+v", got)
	}
}

func TestRankRespectsLimitAndIsStable(t *testing.T) {
	mems := []Memory{
		{Name: "c", Body: "docker"},
		{Name: "a", Body: "docker"},
		{Name: "b", Body: "docker"},
	}
	got := Rank("docker", mems, 2)
	if len(got) != 2 {
		t.Fatalf("limit ignored: got %d", len(got))
	}
	// Identical scores must not shuffle between calls.
	for i := 0; i < 5; i++ {
		again := Rank("docker", mems, 2)
		if again[0].Name != got[0].Name || again[1].Name != got[1].Name {
			t.Fatal("equal scores produced a different order")
		}
	}
	if got[0].Name != "a" {
		t.Errorf("tie broken by %s, want the name order", got[0].Name)
	}
}
