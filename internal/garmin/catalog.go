package garmin

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// exercisesJSON is the Garmin strength-exercise catalog harvested from the live
// Connect web builder. See data/SCHEMA.md. Each row is a
// unique (category, exercise, name) triple; exercise and category are Garmin's
// string keys (e.g. SQUAT / BARBELL_BACK_SQUAT), name is the display label.
//
//go:embed data/exercises.json
var exercisesJSON []byte

// Exercise is one row of the Garmin strength catalog: a display name backed by
// the category and exercise string keys the workout API expects.
type Exercise struct {
	Category string `json:"category"`
	Exercise string `json:"exercise"`
	Name     string `json:"name"`
}

// Catalog resolves human exercise names to the Garmin category/exercise keys a
// strength step carries. It is read-only after construction and safe for
// concurrent use.
type Catalog struct {
	all     []Exercise
	byName  map[string]Exercise // lowercased display name -> Exercise
	byKey   map[string]Exercise // "CATEGORY/EXERCISE" -> Exercise
	byExKey map[string]Exercise // bare EXERCISE key -> Exercise (first wins; ambiguous keys also tracked)
}

// LoadCatalog parses the embedded exercise catalog. It returns an error only if
// the vendored data is malformed, which is a build-time defect.
func LoadCatalog() (*Catalog, error) {
	var rows []Exercise
	if err := json.Unmarshal(exercisesJSON, &rows); err != nil {
		return nil, fmt.Errorf("parsing embedded exercises.json: %w", err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("embedded exercises.json is empty")
	}

	c := &Catalog{
		all:     rows,
		byName:  make(map[string]Exercise, len(rows)),
		byKey:   make(map[string]Exercise, len(rows)),
		byExKey: make(map[string]Exercise, len(rows)),
	}
	for _, e := range rows {
		c.byName[normalize(e.Name)] = e
		c.byKey[e.Category+"/"+e.Exercise] = e
		if _, exists := c.byExKey[e.Exercise]; !exists {
			c.byExKey[e.Exercise] = e
		}
	}
	return c, nil
}

// Len reports the number of catalog entries.
func (c *Catalog) Len() int { return len(c.all) }

// Resolve maps a caller-supplied exercise reference to a catalog entry. It
// accepts, in order of preference: an exact display name (case-insensitive), a
// "CATEGORY/EXERCISE" key, or a bare EXERCISE key. On a miss it returns an error
// listing up to five near matches to guide the caller.
func (c *Catalog) Resolve(query string) (Exercise, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Exercise{}, fmt.Errorf("empty exercise reference")
	}
	if e, ok := c.byName[normalize(q)]; ok {
		return e, nil
	}
	if e, ok := c.byKey[strings.ToUpper(q)]; ok {
		return e, nil
	}
	if e, ok := c.byExKey[strings.ToUpper(strings.ReplaceAll(q, " ", "_"))]; ok {
		return e, nil
	}
	suggestions := c.suggest(q, 5)
	if len(suggestions) > 0 {
		return Exercise{}, fmt.Errorf("unknown exercise %q; did you mean: %s", query, strings.Join(suggestions, ", "))
	}
	return Exercise{}, fmt.Errorf("unknown exercise %q (no close matches in the %d-entry catalog)", query, len(c.all))
}

// Search returns up to limit catalog entries whose normalized display name
// contains the normalized query as a substring, sorted by name. An empty query
// is an error (the catalog is too large to dump whole).
func (c *Catalog) Search(query string, limit int) ([]Exercise, error) {
	needle := normalize(query)
	if needle == "" {
		return nil, fmt.Errorf("query is required (a substring of the exercise name)")
	}
	if limit <= 0 {
		limit = 20
	}
	var hits []Exercise
	for _, e := range c.all {
		if strings.Contains(normalize(e.Name), needle) {
			hits = append(hits, e)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// suggest returns up to limit display names whose normalized form contains the
// normalized query as a substring (cheap, dependency-free fuzzy matching).
func (c *Catalog) suggest(query string, limit int) []string {
	needle := normalize(query)
	var hits []string
	for _, e := range c.all {
		if strings.Contains(normalize(e.Name), needle) {
			hits = append(hits, e.Name)
		}
	}
	sort.Strings(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

// normalize lowercases and collapses separators so "Barbell Back Squat",
// "barbell_back_squat" and "barbell  back squat" all compare equal.
func normalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("_", " ", "-", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
