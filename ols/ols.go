// Package ols is the library behind the ols command line:
// the HTTP client, request shaping, and the typed data models for the
// EMBL-EBI Ontology Lookup Service (OLS4).
//
// The Client here is the spine every command shares. It sets a real
// User-Agent, paces requests so a busy session stays polite, and retries the
// transient failures (429 and 5xx) that any public API throws under load.
// Build your endpoint calls and JSON decoding on top of it.
package ols

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultUserAgent identifies the client to OLS4.
const DefaultUserAgent = "ols-cli/0.1.0 (+https://github.com/tamnd/ols-cli)"

// Host is the OLS4 site this client talks to.
const Host = "www.ebi.ac.uk"

// HostShort is a short display name for the service.
const HostShort = "ols4"

// BaseURL is the root every request is built from.
const BaseURL = "https://www.ebi.ac.uk/ols4/api"

// Client talks to OLS4 over HTTP.
type Client struct {
	HTTP      *http.Client
	UserAgent string
	// Rate is the minimum gap between requests. Zero means no pacing.
	Rate    time.Duration
	Retries int

	last time.Time
}

// NewClient returns a Client with sensible defaults: a 30s timeout, a 300ms
// minimum gap between requests, and three retries on transient errors.
func NewClient() *Client {
	return &Client{
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: DefaultUserAgent,
		Rate:      300 * time.Millisecond,
		Retries:   3,
	}
}

// Get fetches rawURL and returns the response body. It paces and retries
// according to the client's settings. The body is read fully and closed here.
func (c *Client) Get(ctx context.Context, rawURL string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff(attempt)):
			}
		}
		body, retry, err := c.do(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retry {
			return nil, err
		}
	}
	return nil, fmt.Errorf("get %s: %w", rawURL, lastErr)
}

func (c *Client) do(ctx context.Context, rawURL string) (body []byte, retry bool, err error) {
	c.pace()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("http %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("http %d", resp.StatusCode)
	}

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, err
	}
	return b, false, nil
}

// pace blocks until at least Rate has passed since the previous request.
func (c *Client) pace() {
	if c.Rate <= 0 {
		return
	}
	if wait := c.Rate - time.Since(c.last); wait > 0 {
		time.Sleep(wait)
	}
	c.last = time.Now()
}

func backoff(attempt int) time.Duration {
	d := time.Duration(attempt) * 500 * time.Millisecond
	if d > 5*time.Second {
		d = 5 * time.Second
	}
	return d
}

// --- wire types (unexported) ---

type wireSearchDoc struct {
	OboID           string   `json:"obo_id"`
	Label           string   `json:"label"`
	Description     []string `json:"description"`
	OntologyName    string   `json:"ontology_name"`
	OntologyPrefix  string   `json:"ontology_prefix"`
	ExactSynonyms   []string `json:"exact_synonyms"`
	RelatedSynonyms []string `json:"related_synonyms"`
	IsObsolete      bool     `json:"is_obsolete"`
	Type            string   `json:"type"`
}

type wireSearchResp struct {
	Response struct {
		NumFound int             `json:"numFound"`
		Docs     []wireSearchDoc `json:"docs"`
	} `json:"response"`
}

type wireOntologyConfig struct {
	Title           string `json:"title"`
	Description     string `json:"description"`
	Namespace       string `json:"namespace"`
	Version         string `json:"version"`
	PreferredPrefix string `json:"preferredPrefix"`
}

type wireOntology struct {
	OntologyId string             `json:"ontologyId"`
	Config     wireOntologyConfig `json:"config"`
}

type wireOntologyResp struct {
	Page     struct{ TotalElements int `json:"totalElements"` } `json:"page"`
	Embedded struct{ Ontologies []wireOntology `json:"ontologies"` } `json:"_embedded"`
}

// --- public types ---

// Term is a single OLS4 ontology term record.
type Term struct {
	ID          string   `json:"id"          kit:"id"` // obo_id
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Ontology    string   `json:"ontology"`
	Prefix      string   `json:"prefix"`
	Synonyms    []string `json:"synonyms"`
	IsObsolete  bool     `json:"is_obsolete"`
}

// Ontology is a single OLS4 ontology record.
type Ontology struct {
	ID          string `json:"id"          kit:"id"` // ontologyId
	Title       string `json:"title"`
	Description string `json:"description"`
	Prefix      string `json:"prefix"`
	Version     string `json:"version"`
}

// --- client methods ---

// SearchTerms queries the OLS4 search API and returns matching Term records
// along with the total count. If ontology is non-empty, results are filtered
// to that ontology only.
func (c *Client) SearchTerms(ctx context.Context, query, ontology string, limit, offset int) ([]Term, int, error) {
	rawURL := c.searchURL(query, ontology, limit, offset)
	return c.searchTermsURL(ctx, rawURL)
}

// searchURL builds the search endpoint URL. It is the testable core of
// SearchTerms; tests point it at an httptest server without touching BaseURL.
func (c *Client) searchURL(query, ontology string, limit, offset int) string {
	if limit <= 0 {
		limit = 10
	}
	params := url.Values{
		"q":    {query},
		"rows": {strconv.Itoa(limit)},
		"type": {"term"},
	}
	if offset > 0 {
		params.Set("start", strconv.Itoa(offset))
	}
	if ontology != "" {
		params.Set("ontology", strings.ToLower(ontology))
	}
	return BaseURL + "/search?" + params.Encode()
}

func (c *Client) searchTermsURL(ctx context.Context, rawURL string) ([]Term, int, error) {
	body, err := c.Get(ctx, rawURL)
	if err != nil {
		return nil, 0, err
	}
	var ws wireSearchResp
	if err := json.Unmarshal(body, &ws); err != nil {
		return nil, 0, fmt.Errorf("search parse: %w", err)
	}
	out := make([]Term, 0, len(ws.Response.Docs))
	for _, d := range ws.Response.Docs {
		out = append(out, termFromDoc(d))
	}
	return out, ws.Response.NumFound, nil
}

// GetTerm fetches a single ontology term by its OBO ID (e.g. "GO:0051301").
// It uses the search endpoint to resolve the ID to a canonical term record.
// The ontology parameter is used to narrow the search when provided.
func (c *Client) GetTerm(ctx context.Context, oboID, ontology string) (*Term, error) {
	terms, _, err := c.SearchTerms(ctx, `obo_id:"`+oboID+`"`, ontology, 1, 0)
	if err != nil {
		return nil, err
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("term %q not found", oboID)
	}
	return &terms[0], nil
}

// GetOntologies lists ontologies from OLS4. It returns up to limit results.
func (c *Client) GetOntologies(ctx context.Context, limit int) ([]Ontology, int, error) {
	rawURL := c.ontologiesURL(limit)
	return c.ontologiesFromURL(ctx, rawURL)
}

// ontologiesURL builds the ontologies endpoint URL.
func (c *Client) ontologiesURL(limit int) string {
	if limit <= 0 {
		limit = 20
	}
	params := url.Values{
		"size": {strconv.Itoa(limit)},
		"page": {"0"},
	}
	return BaseURL + "/ontologies?" + params.Encode()
}

func (c *Client) ontologiesFromURL(ctx context.Context, rawURL string) ([]Ontology, int, error) {
	body, err := c.Get(ctx, rawURL)
	if err != nil {
		return nil, 0, err
	}
	var ws wireOntologyResp
	if err := json.Unmarshal(body, &ws); err != nil {
		return nil, 0, fmt.Errorf("ontologies parse: %w", err)
	}
	out := make([]Ontology, 0, len(ws.Embedded.Ontologies))
	for _, o := range ws.Embedded.Ontologies {
		out = append(out, ontologyFromWire(o))
	}
	return out, ws.Page.TotalElements, nil
}

// --- helpers ---

// termFromDoc converts a search doc wire type to a Term.
func termFromDoc(d wireSearchDoc) Term {
	desc := ""
	if len(d.Description) > 0 {
		desc = d.Description[0]
	}
	synonyms := append(d.ExactSynonyms, d.RelatedSynonyms...)
	return Term{
		ID:          d.OboID,
		Label:       d.Label,
		Description: desc,
		Ontology:    d.OntologyName,
		Prefix:      d.OntologyPrefix,
		Synonyms:    synonyms,
		IsObsolete:  d.IsObsolete,
	}
}

// ontologyFromWire converts a wire ontology type to an Ontology.
func ontologyFromWire(o wireOntology) Ontology {
	return Ontology{
		ID:          o.OntologyId,
		Title:       o.Config.Title,
		Description: o.Config.Description,
		Prefix:      o.Config.PreferredPrefix,
		Version:     o.Config.Version,
	}
}
