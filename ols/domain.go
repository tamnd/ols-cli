package ols

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/tamnd/any-cli/kit"
	"github.com/tamnd/any-cli/kit/errs"
)

// domain.go exposes OLS4 as a kit Domain: a driver that a multi-domain
// host (ant) enables with a single blank import,
//
//	import _ "github.com/tamnd/ols-cli/ols"
//
// exactly as a database/sql program enables a driver with `import _
// "github.com/lib/pq"`. The init below registers it; the host then dereferences
// ols:// URIs by routing to the operations Register installs. The same
// Domain also builds the standalone ols binary (see cli/root.go), so the
// binary and a host share one source of truth.
func init() { kit.Register(Domain{}) }

// Domain is the OLS4 driver. It carries no state; the per-run client is
// built by the factory Register hands kit.
type Domain struct{}

// Info describes the scheme, the hostnames a pasted link is matched against, and
// the identity reused for the binary's help and version.
func (Domain) Info() kit.DomainInfo {
	return kit.DomainInfo{
		Scheme: "ols",
		Hosts:  []string{Host},
		Identity: kit.Identity{
			Binary: "ols",
			Short:  "Read public ontology terms from EMBL-EBI OLS4.",
			Long: `Read public ontology terms from EMBL-EBI OLS4.

ols reads from the EMBL-EBI Ontology Lookup Service (OLS4) over plain HTTPS,
shapes it into clean records, and prints output that pipes into the rest of
your tools. No API key required. 10.7M terms across 280 ontologies.`,
			Site: "www.ebi.ac.uk/ols4",
			Repo: "https://github.com/tamnd/ols-cli",
		},
	}
}

// Register installs the client factory and every operation onto app.
func (Domain) Register(app *kit.App) {
	app.SetClient(newClient)

	// search: keyword search across ontology terms.
	kit.Handle(app, kit.OpMeta{Name: "search", Group: "read", List: true,
		Summary: "Search ontology terms",
		Args:    []kit.Arg{{Name: "query", Help: "search terms"}}}, searchTerms)

	// term: fetch a single term by OBO ID.
	kit.Handle(app, kit.OpMeta{Name: "term", Group: "read", Single: true,
		Summary: "Fetch a term by OBO ID (e.g. GO:0051301)", URIType: "term", Resolver: true,
		Args: []kit.Arg{{Name: "id", Help: "OBO ID (e.g. GO:0051301)"}}}, getTerm)

	// ontologies: list all ontologies.
	kit.Handle(app, kit.OpMeta{Name: "ontologies", Group: "read", List: true,
		Summary: "List ontologies in OLS4"}, listOntologies)
}

// newClient builds the OLS4 client from the host-resolved config.
func newClient(_ context.Context, cfg kit.Config) (any, error) {
	c := NewClient()
	if cfg.UserAgent != "" {
		c.UserAgent = cfg.UserAgent
	}
	if cfg.Rate > 0 {
		c.Rate = cfg.Rate
	}
	if cfg.Retries > 0 {
		c.Retries = cfg.Retries
	}
	if cfg.Timeout > 0 {
		c.HTTP.Timeout = cfg.Timeout
	}
	return c, nil
}

// --- inputs ---

type searchInput struct {
	Query    string  `kit:"arg" help:"search terms"`
	Ontology string  `kit:"flag" help:"filter to ontology (e.g. go, mondo, chebi)"`
	Limit    int     `kit:"flag,inherit" help:"max results"`
	Offset   int     `kit:"flag" help:"result offset"`
	Client   *Client `kit:"inject"`
}

type termRef struct {
	ID       string  `kit:"arg" help:"OBO ID (e.g. GO:0051301)"`
	Ontology string  `kit:"flag" help:"ontology for disambiguation (e.g. go)"`
	Client   *Client `kit:"inject"`
}

type ontologiesInput struct {
	Limit  int     `kit:"flag,inherit" help:"max results"`
	Client *Client `kit:"inject"`
}

// --- handlers ---

func searchTerms(ctx context.Context, in searchInput, emit func(*Term) error) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 10
	}
	terms, _, err := in.Client.SearchTerms(ctx, in.Query, in.Ontology, limit, in.Offset)
	if err != nil {
		return mapErr(err)
	}
	for i := range terms {
		if err := emit(&terms[i]); err != nil {
			return err
		}
	}
	return nil
}

func getTerm(ctx context.Context, in termRef, emit func(*Term) error) error {
	t, err := in.Client.GetTerm(ctx, in.ID, in.Ontology)
	if err != nil {
		return mapErr(err)
	}
	return emit(t)
}

func listOntologies(ctx context.Context, in ontologiesInput, emit func(*Ontology) error) error {
	limit := in.Limit
	if limit <= 0 {
		limit = 20
	}
	ontologies, _, err := in.Client.GetOntologies(ctx, limit)
	if err != nil {
		return mapErr(err)
	}
	for i := range ontologies {
		if err := emit(&ontologies[i]); err != nil {
			return err
		}
	}
	return nil
}

// --- Resolver: pure string functions, no network ---

// Classify turns any accepted input — a bare OBO ID, ontology short name, or
// full OLS4 URL — into the canonical (type, id).
//
// Rule: if input contains ":" it is a term; otherwise it is an ontology.
func (Domain) Classify(input string) (uriType, id string, err error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errs.Usage("empty OLS4 reference")
	}
	// Strip full URL down to the meaningful part.
	if u, err2 := url.Parse(input); err2 == nil && (u.Scheme == "http" || u.Scheme == "https") {
		// e.g. https://www.ebi.ac.uk/ols4/ontologies/go → "go"
		// e.g. https://www.ebi.ac.uk/ols4/ontologies/go/terms/... → leave as-is
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] == "ols4" && parts[1] == "ontologies" {
			if len(parts) == 3 {
				return "ontology", parts[2], nil
			}
		}
		input = strings.Trim(u.Path, "/")
	}
	if strings.Contains(input, ":") {
		return "term", input, nil
	}
	return "ontology", strings.ToLower(input), nil
}

// Locate is the inverse: the live https URL for a (type, id).
func (Domain) Locate(uriType, id string) (string, error) {
	switch uriType {
	case "term":
		// Build the OLS4 browse URL for a term by converting OBO ID to IRI and double-encoding.
		// OLS4 expects the IRI to be percent-encoded twice: first with QueryEscape (encodes :
		// and / as %3A and %2F), then with QueryEscape again (encodes % as %25), yielding
		// e.g. http%253A%252F%252Fpurl.obolibrary.org%252Fobo%252FGO_0051301.
		iri := oboIDToIRI(id)
		encoded := url.QueryEscape(url.QueryEscape(iri))
		// Derive the ontology prefix from the OBO ID (e.g. "GO" from "GO:0051301").
		prefix := strings.ToLower(oboPrefix(id))
		if prefix == "" {
			return fmt.Sprintf("https://%s/ols4/search?q=%s&type=term", Host, url.QueryEscape(id)), nil
		}
		return fmt.Sprintf("https://%s/ols4/ontologies/%s/terms/%s", Host, prefix, encoded), nil
	case "ontology":
		return fmt.Sprintf("https://%s/ols4/ontologies/%s", Host, strings.ToLower(id)), nil
	default:
		return "", errs.Usage("ols has no resource type %q", uriType)
	}
}

// --- helpers ---

// oboIDToIRI converts an OBO ID like "GO:0051301" to its canonical IRI
// "http://purl.obolibrary.org/obo/GO_0051301".
func oboIDToIRI(oboID string) string {
	return "http://purl.obolibrary.org/obo/" + strings.ReplaceAll(oboID, ":", "_")
}

// oboPrefix returns the prefix part of an OBO ID (e.g. "GO" from "GO:0051301").
func oboPrefix(oboID string) string {
	if i := strings.Index(oboID, ":"); i >= 0 {
		return oboID[:i]
	}
	return ""
}

// mapErr converts a library error into the kit error kind that carries the right
// exit code.
func mapErr(err error) error {
	return err
}
