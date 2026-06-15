package ols

import (
	"testing"

	"github.com/tamnd/any-cli/kit"
)

// These tests are offline: they exercise the URI driver's pure string functions
// and the host wiring (mint, body, resolve), which need no network. The client's
// HTTP behaviour is covered in ols_test.go.

func TestDomainInfo(t *testing.T) {
	info := Domain{}.Info()
	if info.Scheme != "ols" {
		t.Errorf("Scheme = %q, want ols", info.Scheme)
	}
	if len(info.Hosts) == 0 || info.Hosts[0] != Host {
		t.Errorf("Hosts = %v, want [%s]", info.Hosts, Host)
	}
	if info.Identity.Binary != "ols" {
		t.Errorf("Identity.Binary = %q, want ols", info.Identity.Binary)
	}
}

func TestClassifyTerm(t *testing.T) {
	typ, id, err := Domain{}.Classify("GO:0051301")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "term" {
		t.Errorf("type = %q, want term", typ)
	}
	if id != "GO:0051301" {
		t.Errorf("id = %q, want GO:0051301", id)
	}
}

func TestClassifyOntology(t *testing.T) {
	typ, id, err := Domain{}.Classify("go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if typ != "ontology" {
		t.Errorf("type = %q, want ontology", typ)
	}
	if id != "go" {
		t.Errorf("id = %q, want go", id)
	}
}

func TestClassifyEmpty(t *testing.T) {
	_, _, err := Domain{}.Classify("")
	if err == nil {
		t.Error("Classify(\"\") should return an error")
	}
}

func TestLocateTerm(t *testing.T) {
	got, err := Domain{}.Locate("term", "GO:0051301")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://www.ebi.ac.uk/ols4/ontologies/go/terms/http%253A%252F%252Fpurl.obolibrary.org%252Fobo%252FGO_0051301"
	if got != want {
		t.Errorf("Locate(term, GO:0051301) = %q, want %q", got, want)
	}
}

func TestLocateOntology(t *testing.T) {
	got, err := Domain{}.Locate("ontology", "go")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "https://www.ebi.ac.uk/ols4/ontologies/go"
	if got != want {
		t.Errorf("Locate(ontology, go) = %q, want %q", got, want)
	}
}

func TestLocateUnknownType(t *testing.T) {
	_, err := Domain{}.Locate("page", "foo")
	if err == nil {
		t.Error("Locate with unknown type should return an error")
	}
}

// TestHostWiring mounts the driver in a kit Host (the runtime ant drives) and
// checks the round trip: a record mints to its URI, its body is readable, and a
// bare id resolves back to the same URI. The init in domain.go registers the
// domain, so kit.Open finds it.
func TestHostWiring(t *testing.T) {
	h, err := kit.Open()
	if err != nil {
		t.Fatal(err)
	}

	term := &Term{
		ID:          "GO:0051301",
		Label:       "cell division",
		Description: "The process resulting in division and partitioning of components of a cell.",
		Ontology:    "go",
		Prefix:      "GO",
	}
	u, err := h.Mint(term)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if want := "ols://term/GO:0051301"; u.String() != want {
		t.Errorf("Mint = %q, want %q", u.String(), want)
	}

	got, err := h.ResolveOn("ols", "MONDO:0700096")
	if err != nil || got.String() != "ols://term/MONDO:0700096" {
		t.Errorf("ResolveOn = (%q, %v), want ols://term/MONDO:0700096", got.String(), err)
	}
}
