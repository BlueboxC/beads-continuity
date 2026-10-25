package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/httpapi/bdpwire"
	"github.com/steveyegge/beads/internal/httpapi/graphread"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

type graphReadRoute struct {
	kind, path string
	selection  *graphread.Selection
}

// Compile syntax only after exact catalog resolution. Installed Type URLs are
// reserved individually; an arbitrary types/... or links/... ID can be a Bead.
func (g *GraphRead) resolveRoute(ctx context.Context, path string, parameters url.Values) (graphReadRoute, error) {
	switch path {
	case "", "bdp.json", "beads/", "links/", "types/":
		return g.compileRoute(path, parameters)
	}
	if graph.ValidateCanonicalResourceID(path) != nil {
		return graphReadRoute{}, graphstore.ErrNotFound
	}
	identity, err := g.reader.Identity(ctx, path)
	if errors.Is(err, graphstore.ErrNotFound) && strings.HasPrefix(path, "types/") {
		return g.compileRoute(path, parameters)
	}
	if err != nil {
		return graphReadRoute{}, err
	}
	if identity.State != "live" && parameters.Get("cursor") == "" {
		return graphReadRoute{}, graphstore.ErrNotFound
	}
	return g.compileResourceRoute(path, parameters, identity.Kind)
}

// Inspect the original escaped spelling before any ServeMux/URL normalization.
// Transport aliases affect only reachability: this function never derives the
// persisted Scope origin from Host, forwarded headers or an absolute target.
func (g *GraphRead) requestTarget(r *http.Request) (string, url.Values, bdpwire.ReadProblemCode) {
	target := r.RequestURI
	if target == "" {
		target = r.URL.RequestURI()
	}
	if len(target) > graphReadTargetLimit {
		return "", nil, bdpwire.CodeRequestTooLarge
	}
	if strings.ContainsAny(target, "#\\") || r.URL.User != nil || r.URL.Opaque != "" {
		return "", nil, bdpwire.CodeResourceNotFound
	}
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		start := strings.Index(target, "://") + 3
		end := strings.IndexAny(target[start:], "/?")
		if end < 0 {
			target = "/"
		} else if target[start+end] == '?' {
			target = "/" + target[start+end:]
		} else {
			target = target[start+end:]
		}
	}
	rawPath, rawQuery, _ := strings.Cut(target, "?")
	if !strings.HasPrefix(rawPath, "/") || strings.HasPrefix(rawPath, "//") || rawPath != r.URL.EscapedPath() {
		return "", nil, bdpwire.CodeResourceNotFound
	}
	if !strings.HasPrefix(rawPath, g.path) {
		return "", nil, bdpwire.CodeResourceNotFound
	}
	path := strings.TrimPrefix(rawPath, g.path)
	parameters, err := url.ParseQuery(rawQuery)
	if err != nil {
		return "", nil, bdpwire.CodeInvalidParameter
	}
	for key, values := range parameters {
		if !utf8.ValidString(key) {
			return "", nil, bdpwire.CodeInvalidParameter
		}
		for _, value := range values {
			if !utf8.ValidString(value) {
				return "", nil, bdpwire.CodeInvalidParameter
			}
		}
	}
	return path, parameters, ""
}

func (g *GraphRead) compileRoute(path string, parameters url.Values) (graphReadRoute, error) {
	result := graphReadRoute{path: path}
	switch path {
	case "", "bdp.json":
		if err := graphReadParameters(parameters); err != nil {
			return result, err
		}
		result.kind = "scope"
		if path != "" {
			result.kind = "discovery"
		}
		return result, nil
	case "beads/", "links/", "types/":
		q, err := graphread.CompileCollection(g.scope, strings.TrimSuffix(path, "/"), parameters, g.limits)
		if err != nil {
			return result, err
		}
		return g.selectionRoute("selection", path, q)
	}
	root, _, hasPath := strings.Cut(path, "/")
	if !hasPath {
		return result, graphstore.ErrNotFound
	}
	switch root {
	case "beads":
		if graph.ValidateBeadPath(path) != nil {
			return result, graphstore.ErrNotFound
		}
	case "links":
		if graph.ValidateLinkPath(path) != nil {
			return result, graphstore.ErrNotFound
		}
	case "types":
		if graph.ValidateBeadPath("beads/"+strings.TrimPrefix(path, "types/")) != nil {
			return result, graphstore.ErrNotFound
		}
	default:
		return result, graphstore.ErrNotFound
	}
	if root == "types" {
		if err := graphReadParameters(parameters); err != nil {
			return result, err
		}
		result.kind = "type"
		return result, nil
	}
	kind := graph.KindBead
	if root == "links" {
		kind = graph.KindLink
	}
	return g.compileResourceRoute(path, parameters, kind)
}

func (g *GraphRead) compileResourceRoute(path string, parameters url.Values, kind graph.ResourceKind) (graphReadRoute, error) {
	result := graphReadRoute{path: path}
	if kind != graph.KindBead && kind != graph.KindLink {
		return result, graphstore.ErrInvalidStore
	}
	if values, exists := parameters["include"]; exists {
		if kind != graph.KindBead || len(values) != 1 || values[0] != "links" {
			return result, &graphread.ParameterError{Name: "include"}
		}
		if err := graphReadParameters(parameters, "include", "direction", "limit"); err != nil {
			return result, err
		}
		incident := url.Values{"view": []string{"links"}}
		for key, values := range parameters {
			if key != "include" {
				incident[key] = values
			}
		}
		q, err := graphread.CompileIncident(g.scope, path, incident, g.limits)
		if err != nil {
			return result, err
		}
		return g.selectionRoute("aggregate", path, q)
	}
	values, hasView := parameters["view"]
	if !hasView {
		if err := graphReadParameters(parameters); err != nil {
			return result, err
		}
		result.kind = "resource"
		return result, nil
	}
	if len(values) != 1 {
		return result, &graphread.ParameterError{Name: "view"}
	}
	switch values[0] {
	case "properties":
		if err := graphReadParameters(parameters, "view"); err != nil {
			return result, err
		}
		result.kind = "properties"
		return result, nil
	case "links":
		if kind != graph.KindBead {
			return result, &graphread.ParameterError{Name: "view"}
		}
		q, err := graphread.CompileIncident(g.scope, path, parameters, g.limits)
		if err != nil {
			return result, err
		}
		return g.selectionRoute("selection", path, q)
	}
	return result, &graphread.ParameterError{Name: "view"}
}

func (g *GraphRead) selectionRoute(kind, path string, q *graphread.Selection) (graphReadRoute, error) {
	if _, err := g.pager.ValidateLimit(q.Limit()); err != nil {
		return graphReadRoute{}, err
	}
	return graphReadRoute{kind: kind, path: path, selection: q}, nil
}
func graphReadParameters(parameters url.Values, allowed ...string) error {
	permitted := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		permitted[key] = true
	}
	for key, values := range parameters {
		if !permitted[key] || len(values) != 1 {
			return &graphread.ParameterError{Name: key}
		}
	}
	return nil
}
