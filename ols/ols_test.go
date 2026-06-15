package ols

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSearchTerms(t *testing.T) {
	resp := wireSearchResp{}
	resp.Response.NumFound = 2
	resp.Response.Docs = []wireSearchDoc{
		{
			OboID:          "GO:0051301",
			Label:          "cell division",
			Description:    []string{"The process resulting in division and partitioning of components of a cell."},
			OntologyName:   "go",
			OntologyPrefix: "GO",
			ExactSynonyms:  []string{"cell division process"},
			IsObsolete:     false,
			Type:           "term",
		},
		{
			OboID:          "GO:0007049",
			Label:          "cell cycle",
			Description:    []string{"The progression of biochemical and morphological phases and events."},
			OntologyName:   "go",
			OntologyPrefix: "GO",
			IsObsolete:     false,
			Type:           "term",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("q") == "" {
			t.Error("missing q param")
		}
		if q.Get("type") != "term" {
			t.Errorf("type param = %q, want term", q.Get("type"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	terms, total, err := c.searchTermsURL(context.Background(), srv.URL+"?q=cell+division&type=term")
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if len(terms) != 2 {
		t.Fatalf("len(terms) = %d, want 2", len(terms))
	}
	if terms[0].Label != "cell division" {
		t.Errorf("Label = %q, want cell division", terms[0].Label)
	}
	if terms[0].Ontology != "go" {
		t.Errorf("Ontology = %q, want go", terms[0].Ontology)
	}
	if terms[1].Label != "cell cycle" {
		t.Errorf("Label = %q, want cell cycle", terms[1].Label)
	}
}

func TestGetTerm(t *testing.T) {
	resp := wireSearchResp{}
	resp.Response.NumFound = 1
	resp.Response.Docs = []wireSearchDoc{
		{
			OboID:          "GO:0051301",
			Label:          "cell division",
			Description:    []string{"The process resulting in division and partitioning of components of a cell."},
			OntologyName:   "go",
			OntologyPrefix: "GO",
			IsObsolete:     false,
			Type:           "term",
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	terms, _, err := c.searchTermsURL(context.Background(), srv.URL+"?q=obo_id%3A%22GO%3A0051301%22&type=term")
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 1 {
		t.Fatalf("len(terms) = %d, want 1", len(terms))
	}
	if terms[0].ID != "GO:0051301" {
		t.Errorf("ID = %q, want GO:0051301", terms[0].ID)
	}
	if terms[0].Label != "cell division" {
		t.Errorf("Label = %q, want cell division", terms[0].Label)
	}
}

func TestGetOntologies(t *testing.T) {
	resp := wireOntologyResp{}
	resp.Page.TotalElements = 280
	resp.Embedded.Ontologies = []wireOntology{
		{
			OntologyId: "go",
			Config: wireOntologyConfig{
				Title:           "Gene Ontology",
				Description:     "The Gene Ontology (GO) project is a major bioinformatics initiative.",
				Namespace:       "go",
				Version:         "2024-01-17",
				PreferredPrefix: "GO",
			},
		},
		{
			OntologyId: "mondo",
			Config: wireOntologyConfig{
				Title:           "MONDO Disease Ontology",
				Description:     "A semi-automatically constructed ontology that merges in multiple disease resources.",
				Namespace:       "mondo",
				Version:         "2024-01-03",
				PreferredPrefix: "MONDO",
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ontologies" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	ontologies, total, err := c.ontologiesFromURL(context.Background(), srv.URL+"/ontologies?size=20&page=0")
	if err != nil {
		t.Fatal(err)
	}
	if total != 280 {
		t.Errorf("total = %d, want 280", total)
	}
	if len(ontologies) != 2 {
		t.Fatalf("len(ontologies) = %d, want 2", len(ontologies))
	}
	if ontologies[0].Title != "Gene Ontology" {
		t.Errorf("Title = %q, want Gene Ontology", ontologies[0].Title)
	}
	if ontologies[1].Title != "MONDO Disease Ontology" {
		t.Errorf("Title = %q, want MONDO Disease Ontology", ontologies[1].Title)
	}
}

func TestRetryOn503(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("recovered"))
	}))
	defer srv.Close()

	c := NewClient()
	c.Rate = 0
	c.Retries = 5

	start := time.Now()
	body, err := c.Get(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "recovered" {
		t.Errorf("body = %q after retries", body)
	}
	if hits != 3 {
		t.Errorf("server saw %d hits, want 3", hits)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Error("retries did not back off")
	}
}
